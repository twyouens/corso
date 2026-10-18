package m365

import (
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/tester/tconfig"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// concurrencyLimitReporter is satisfied by the request adapters
// graph.CreateAdapter builds; graph's adapter type is unexported, so the
// method is reachable only by structural assertion.
type concurrencyLimitReporter interface {
	ConcurrencyLimit() int
}

func controllerConcurrencyLimit(t *testing.T, svc graph.Servicer) int {
	t.Helper()

	reporter, ok := svc.Adapter().(concurrencyLimitReporter)
	require.True(t, ok, "graph adapter reports its concurrency limit")

	return reporter.ConcurrencyLimit()
}

type ControllerConcurrencyUnitSuite struct {
	tester.Suite
}

func TestControllerConcurrencyUnitSuite(t *testing.T) {
	suite.Run(t, &ControllerConcurrencyUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// TestNewController_ConcurrencyLimitIsPerControllerNotPerProcess covers the
// call site the process-global limiter actually lived at.
//
// NewController used to call graph.InitializeConcurrencyLimiter - a
// sync.Once - as its very first statement, before it read the account's
// credentials, with enable = (service == path.ExchangeService) and capacity =
// co.Parallelism.ItemFetch.  Three consequences, all reproduced here and all
// now gone:
//
//  1. The first controller built in the process decided the capacity for
//     every Graph client built afterwards.
//  2. A controller that failed before it had credentials still decided it,
//     because the latch ran before the failure.
//  3. A controller for a non-Exchange service disabled the limiter outright
//     for the whole process, leaving every later Graph client unlimited.
//
// Every controller here carries its own capacity, taken from its own
// control.Options, and every one of them is limited whatever its service.
func (suite *ControllerConcurrencyUnitSuite) TestNewController_ConcurrencyLimitIsPerControllerNotPerProcess() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	acct := tconfig.NewFakeM365Account(t)

	opts := func(itemFetch int) control.Options {
		co := control.DefaultOptions()
		co.Parallelism.ItemFetch = itemFetch

		return co
	}

	// 2: a controller that cannot read its credentials fails, and sets
	// nothing.  account.Account's zero value has no M365 config, so this
	// fails at exactly the point the latch used to have already run past.
	failed, err := NewController(ctx, account.Account{}, path.OneDriveService, opts(1), count.New())
	require.Error(t, err, "building a controller with no usable account config")
	require.Nil(t, failed, "a failed controller")

	// 3: a non-Exchange controller first.  Under the old code this nilled
	// the limiter singleton for the process; now it is simply a controller
	// with its own capacity.
	oneDrive, err := NewController(ctx, acct, path.OneDriveService, opts(2), count.New())
	require.NoError(t, err, clues.ToCore(err))

	// 1: later controllers, wider and narrower, each with their own.
	exchange, err := NewController(ctx, acct, path.ExchangeService, opts(4), count.New())
	require.NoError(t, err, clues.ToCore(err))

	unknown, err := NewController(ctx, acct, path.UnknownService, opts(1), count.New())
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, 2, controllerConcurrencyLimit(t, oneDrive.AC.Stable),
		"the OneDrive controller built first")
	assert.Equal(t, 4, controllerConcurrencyLimit(t, exchange.AC.Stable),
		"the Exchange controller built second is not capped by the first")
	assert.Equal(t, 1, controllerConcurrencyLimit(t, unknown.AC.Stable),
		"the path.UnknownService controller pkg/engine opens a repository with")

	// Every controller is limited, whatever its service: the old code's
	// enable=false branch, reachable for any non-Exchange service, left
	// later clients with no limiter at all.
	for _, tc := range []struct {
		name string
		ctrl *Controller
	}{
		{"onedrive", oneDrive},
		{"exchange", exchange},
		{"unknown service", unknown},
	} {
		assert.Positive(t, controllerConcurrencyLimit(t, tc.ctrl.AC.Stable),
			"%s controller is concurrency limited", tc.name)
		assert.LessOrEqual(t, controllerConcurrencyLimit(t, tc.ctrl.AC.Stable), 4,
			"%s controller's capacity stays within the clamp", tc.name)
	}
}
