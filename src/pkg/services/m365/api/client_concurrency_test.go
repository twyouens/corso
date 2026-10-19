package api

import (
	"testing"
	"time"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/tester/tconfig"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// concurrencyLimitReporter is satisfied by the request adapters
// graph.CreateAdapter builds.  Declared here rather than imported because
// graph's adapter type is unexported: the method is reachable only by
// structural assertion, which is deliberate - nothing in non-test code reads
// it.
type concurrencyLimitReporter interface {
	ConcurrencyLimit() int
}

func concurrencyLimitOf(t *testing.T, svc graph.Servicer) int {
	t.Helper()

	reporter, ok := svc.Adapter().(concurrencyLimitReporter)
	require.True(t, ok, "graph adapter reports its concurrency limit")

	return reporter.ConcurrencyLimit()
}

type ClientConcurrencyUnitSuite struct {
	tester.Suite
}

func TestClientConcurrencyUnitSuite(t *testing.T) {
	suite.Run(t, &ClientConcurrencyUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// TestNewClient_ConcurrencyLimitComesFromItsOwnOptions pins the wiring this
// chunk exists for: the Graph concurrency capacity is a property of the
// client, taken from the control.Options that client's caller passed, not
// from a process-global value set by whoever ran first.
//
// Both of the clients api.NewClient builds on the kiota middleware chain are
// checked.  The Requester is not: it runs on graph.NewHTTPWrapper's chain,
// which carries no concurrency limiter at all - see the comment on
// graph.internalMiddleware and on NewClient's rqr.
func (suite *ClientConcurrencyUnitSuite) TestNewClient_ConcurrencyLimitComesFromItsOwnOptions() {
	table := []struct {
		name      string
		itemFetch int
		expect    int
	}{
		{name: "caller asked for one", itemFetch: 1, expect: 1},
		{name: "caller asked for two", itemFetch: 2, expect: 2},
		{name: "caller asked for the maximum", itemFetch: 4, expect: 4},
		{name: "caller asked for more than the maximum", itemFetch: 9001, expect: 4},
		{name: "caller never set parallelism", itemFetch: 0, expect: 4},
		{name: "caller set a nonsense parallelism", itemFetch: -3, expect: 4},
	}

	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			creds := fakeCreds(t)

			opts := control.DefaultOptions()
			opts.Parallelism.ItemFetch = test.itemFetch

			cli, err := NewClient(creds, opts, count.New())
			require.NoError(t, err, clues.ToCore(err))

			assert.Equal(t, test.expect, concurrencyLimitOf(t, cli.Stable), "Stable service")
			assert.Equal(t, test.expect, concurrencyLimitOf(t, cli.LargeItem), "LargeItem service")
		})
	}
}

// TestNewClient_ExplicitOptionOverridesTheDerivedLimit proves the derived
// limit is a default a caller can still override, rather than a value that
// silently wins: options are applied in order and NewClient prepends its own.
func (suite *ClientConcurrencyUnitSuite) TestNewClient_ExplicitOptionOverridesTheDerivedLimit() {
	t := suite.T()

	opts := control.DefaultOptions()
	opts.Parallelism.ItemFetch = 4

	cli, err := NewClient(fakeCreds(t), opts, count.New(), graph.ConcurrencyLimit(2))
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, 2, concurrencyLimitOf(t, cli.Stable), "Stable service")
	assert.Equal(t, 2, concurrencyLimitOf(t, cli.LargeItem), "LargeItem service")
}

// TestNewClient_AFailedClientSetsNothingForLaterClients reproduces the exact
// shape of the bug this chunk removes.
//
// Under the process-global limiter, the capacity was latched by the *first*
// repository opened in the process - including one that never got as far as a
// successful Microsoft 365 connection, because m365.NewController called
// graph.InitializeConcurrencyLimiter before it read the account's credentials
// at all.  A failed open therefore decided the clamp for every later,
// successful open in that process, and nothing could change it afterwards.
//
// Here the first client is built with a narrow capacity and credentials that
// cannot authenticate - blank ones, which fail inside the Azure identity
// credential before any network call, so this stays a unit test - and the
// second is built successfully with a wide capacity.  The second gets its
// own.  Then the orders are reversed, because a latch is order-dependent.
func (suite *ClientConcurrencyUnitSuite) TestNewClient_AFailedClientSetsNothingForLaterClients() {
	t := suite.T()

	// Blank credentials: azidentity rejects an empty tenant id when the
	// client secret credential is constructed, before any network call, so
	// this is the authentication failure mode without a network dependency.
	unauthenticatable := account.M365Config{}

	narrow := control.DefaultOptions()
	narrow.Parallelism.ItemFetch = 1

	wide := control.DefaultOptions()
	wide.Parallelism.ItemFetch = 4

	// A failing open at capacity 1, then a succeeding open at capacity 4.
	failed, err := NewClient(unauthenticatable, narrow, count.New())
	require.Error(t, err, "building a client with credentials that cannot authenticate")
	require.Nil(t, failed.Stable, "a failed client holds no service")

	succeeded, err := NewClient(fakeCreds(t), wide, count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(
		t,
		4,
		concurrencyLimitOf(t, succeeded.Stable),
		"a client built after a failed one gets its own capacity, not the failed one's")

	// And the reverse order: a failing open at capacity 4 must not widen a
	// later narrow one either.
	failed, err = NewClient(unauthenticatable, wide, count.New())
	require.Error(t, err, "building a client with credentials that cannot authenticate")
	require.Nil(t, failed.Stable, "a failed client holds no service")

	succeeded, err = NewClient(fakeCreds(t), narrow, count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(
		t,
		1,
		concurrencyLimitOf(t, succeeded.Stable),
		"a client built after a failed one gets its own capacity, not the failed one's")
}

// TestNewClient_TwoClientsInOneProcessDoNotShareCapacity is the api-level
// statement of the same property: whichever order they are built in, each
// client reports its own capacity.
func (suite *ClientConcurrencyUnitSuite) TestNewClient_TwoClientsInOneProcessDoNotShareCapacity() {
	t := suite.T()

	narrow := control.DefaultOptions()
	narrow.Parallelism.ItemFetch = 1

	wide := control.DefaultOptions()
	wide.Parallelism.ItemFetch = 4

	narrowFirst, err := NewClient(fakeCreds(t), narrow, count.New())
	require.NoError(t, err, clues.ToCore(err))

	wideSecond, err := NewClient(fakeCreds(t), wide, count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, 1, concurrencyLimitOf(t, narrowFirst.Stable), "narrow client, built first")
	assert.Equal(t, 4, concurrencyLimitOf(t, wideSecond.Stable), "wide client, built second")

	wideFirst, err := NewClient(fakeCreds(t), wide, count.New())
	require.NoError(t, err, clues.ToCore(err))

	narrowSecond, err := NewClient(fakeCreds(t), narrow, count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, 4, concurrencyLimitOf(t, wideFirst.Stable), "wide client, built first")
	assert.Equal(t, 1, concurrencyLimitOf(t, narrowSecond.Stable), "narrow client, built second")
}

// httpClientTimeoutReporter is satisfied by the request adapters
// graph.CreateAdapter builds, structurally, for the same reason as
// concurrencyLimitReporter.
type httpClientTimeoutReporter interface {
	HTTPClientTimeout() time.Duration
}

func httpClientTimeoutOf(t *testing.T, svc graph.Servicer) time.Duration {
	t.Helper()

	reporter, ok := svc.Adapter().(httpClientTimeoutReporter)
	require.True(t, ok, "graph adapter reports its http client's timeout")

	return reporter.HTTPClientTimeout()
}

// TestNewClient_LargeItemKeepsNoTimeoutOverACallerTimeout guards the one risk
// of newLargeItemService forwarding its caller's whole option slice: a
// caller-supplied graph.Timeout must not reach the large-item client, whose
// only reason to exist is that it has no timeout.  newLargeItemService
// prevents that by appending graph.NoTimeout() *after* the caller's options.
//
// This is the call-site guard.  graph's TestPopulateConfig_LastOptionWins
// pins the ordering semantics this depends on, but it lives in graph and
// cannot see the order newLargeItemService builds its slice in - with
// NoTimeout() moved to the front of that slice, it stays green while a
// caller's one-second Timeout silently caps every large-item download.  This
// test reads the timeout off LargeItem's actual http.Client, so that
// regression reddens it.
//
// The expected no-timeout value is taken from a client built with
// graph.NoTimeout() alone rather than hard-coded, and Stable is asserted to
// honour the caller's Timeout, so the test proves both that caller options
// reach this client and that the one which must not, does not.
func (suite *ClientConcurrencyUnitSuite) TestNewClient_LargeItemKeepsNoTimeoutOverACallerTimeout() {
	t := suite.T()

	creds := fakeCreds(t)

	reference, err := NewService(creds, count.New(), graph.NoTimeout())
	require.NoError(t, err, clues.ToCore(err))

	noTimeout := httpClientTimeoutOf(t, reference)
	require.Greater(t, noTimeout, time.Hour, "graph.NoTimeout()'s timeout, as applied to a real client")

	cli, err := NewClient(creds, control.DefaultOptions(), count.New(), graph.Timeout(time.Second))
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, time.Second, httpClientTimeoutOf(t, cli.Stable),
		"Stable honours the caller's Timeout, so caller options do reach NewClient's services")
	assert.Equal(t, noTimeout, httpClientTimeoutOf(t, cli.LargeItem),
		"LargeItem keeps NoTimeout even when the caller passed a Timeout")

	// And with no caller options at all, LargeItem is still the no-timeout
	// client, so the assertion above is not satisfied by accident.
	plain, err := NewClient(creds, control.DefaultOptions(), count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, noTimeout, httpClientTimeoutOf(t, plain.LargeItem),
		"LargeItem with no caller options")
	assert.Less(t, httpClientTimeoutOf(t, plain.Stable), noTimeout,
		"Stable keeps its ordinary timeout with no caller options")
}

func fakeCreds(t *testing.T) account.M365Config {
	t.Helper()

	creds, err := tconfig.NewFakeM365Account(t).M365Config()
	require.NoError(t, err, clues.ToCore(err))

	return creds
}
