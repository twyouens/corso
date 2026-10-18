package graph

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	msgraphsdkgo "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/count"
)

// ---------------------------------------------------------------------------
// overlap recorder
//
// The concurrency limiter's only observable effect is how many requests one
// client has in flight at the same time, so - following this repo's
// convention of measuring a claim from the endpoint's side rather than from
// the implementation's own state (see pkg/storage/testdata's S3 request
// recorder) - these tests measure it at the server.
//
// One recorder is shared by every server in a test, so it can report both
// each client's own peak overlap and the peak across all of them at once.
// The cross-client peak is the assertion that actually distinguishes
// per-client limiters from a shared one: two independent limiters of 1 and 4
// can reach 5 requests in flight simultaneously, and no single shared
// semaphore of capacity <= 4 ever can.
// ---------------------------------------------------------------------------

type overlapRecorder struct {
	mu sync.Mutex
	// in-flight and peak request counts, per server label.
	inFlight map[string]int
	peak     map[string]int
	// in-flight and peak request counts across every server.
	totalInFlight int
	totalPeak     int

	// target is the cross-server overlap each handler waits to observe
	// before releasing its slot.  When the limiters under test really are
	// independent, every slot fills, target is reached, and every handler
	// returns immediately.  When they are not, handlers wait out hold
	// instead and the peak assertions fail - slowly, but honestly.
	target int
	hold   time.Duration
}

func newOverlapRecorder(target int, hold time.Duration) *overlapRecorder {
	return &overlapRecorder{
		inFlight: map[string]int{},
		peak:     map[string]int{},
		target:   target,
		hold:     hold,
	}
}

func (or *overlapRecorder) enter(label string) {
	or.mu.Lock()
	defer or.mu.Unlock()

	or.inFlight[label]++
	or.totalInFlight++

	if or.inFlight[label] > or.peak[label] {
		or.peak[label] = or.inFlight[label]
	}

	if or.totalInFlight > or.totalPeak {
		or.totalPeak = or.totalInFlight
	}
}

func (or *overlapRecorder) leave(label string) {
	or.mu.Lock()
	defer or.mu.Unlock()

	or.inFlight[label]--
	or.totalInFlight--
}

func (or *overlapRecorder) reachedTarget() bool {
	or.mu.Lock()
	defer or.mu.Unlock()

	return or.totalPeak >= or.target
}

func (or *overlapRecorder) peakFor(label string) int {
	or.mu.Lock()
	defer or.mu.Unlock()

	return or.peak[label]
}

func (or *overlapRecorder) crossClientPeak() int {
	or.mu.Lock()
	defer or.mu.Unlock()

	return or.totalPeak
}

// server returns a loopback http server that records overlap under label.
// Each request holds its slot until the recorder's cross-server target has
// been observed, or until hold elapses.
func (or *overlapRecorder) server(t *testing.T, label string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		or.enter(label)
		defer or.leave(label)

		deadline := time.Now().Add(or.hold)
		for !or.reachedTarget() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}

		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(srv.Close)

	return srv
}

// hammer fires requestCount concurrent GETs at url through client and waits
// for all of them.
//
// Every request carries ctx, which must be a logger-seeded context (one from
// tester.NewContext).  That is load-bearing under -race, and not about this
// chunk: pkg/logger.singleton lazily writes package-level state - loggerton,
// logCore and clues.SetHasher - with no synchronisation, so the first
// logger.Ctx call on a context with no logger in it races against any
// concurrent one.  graph's LoggingMiddleware calls logger.Ctx on the
// request's context for every response, so concurrent requests on a
// background context trip that pre-existing race.  Passing a seeded context
// means the logger is read from the context and the lazy singleton is never
// reached.
func hammer(ctx context.Context, t *testing.T, client *http.Client, url string, requestCount int) {
	t.Helper()

	var wg sync.WaitGroup

	for i := 0; i < requestCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if !assert.NoError(t, err) {
				return
			}

			resp, err := client.Do(req)
			if !assert.NoError(t, err) {
				return
			}

			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}()
	}

	wg.Wait()
}

type PerClientConcurrencyLimiterUnitSuite struct {
	tester.Suite
}

func TestPerClientConcurrencyLimiterUnitSuite(t *testing.T) {
	suite.Run(t, &PerClientConcurrencyLimiterUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// ---------------------------------------------------------------------------
// criterion 1: two clients in one process get independent limiters
// ---------------------------------------------------------------------------

// TestConcurrencyLimiter_TwoClientsGetIndependentLimiters builds one client at
// capacity 1 and one at capacity 4 in the same process, drives both at the
// same time, and asserts each is bounded by its own capacity.
//
// The three assertions are deliberately different in kind:
//   - the capacity-1 client's peak is exactly 1 (it is limited, and limited
//     to its own value, not to the other client's wider one);
//   - the capacity-4 client's peak is exactly 4 (it is not dragged down to
//     whichever capacity was created first, and it is still limited);
//   - the peak across both clients at once is exactly 5, which no single
//     shared semaphore of capacity <= maxConcurrentRequests can produce.
//
// Both creation orders are exercised, because "first caller wins" is an
// order-dependent bug.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestConcurrencyLimiter_TwoClientsGetIndependentLimiters() {
	const (
		narrow       = 1
		wide         = 4
		requestCount = 12
	)

	table := []struct {
		name  string
		first int
	}{
		{name: "narrow client built first", first: narrow},
		{name: "wide client built first", first: wide},
	}

	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			var narrowClient, wideClient *http.Client

			// Build in the order under test.  With a process-global,
			// first-caller-wins limiter, whichever of these ran first decided
			// the capacity for the other.
			if test.first == narrow {
				narrowClient, _ = KiotaHTTPClient(count.New(), ConcurrencyLimit(narrow))
				wideClient, _ = KiotaHTTPClient(count.New(), ConcurrencyLimit(wide))
			} else {
				wideClient, _ = KiotaHTTPClient(count.New(), ConcurrencyLimit(wide))
				narrowClient, _ = KiotaHTTPClient(count.New(), ConcurrencyLimit(narrow))
			}

			rec := newOverlapRecorder(narrow+wide, 2*time.Second)
			narrowSrv := rec.server(t, "narrow")
			wideSrv := rec.server(t, "wide")

			var wg sync.WaitGroup

			wg.Add(2)

			go func() {
				defer wg.Done()
				hammer(ctx, t, narrowClient, narrowSrv.URL, requestCount)
			}()

			go func() {
				defer wg.Done()
				hammer(ctx, t, wideClient, wideSrv.URL, requestCount)
			}()

			wg.Wait()

			assert.Equal(
				t,
				narrow,
				rec.peakFor("narrow"),
				"peak in-flight requests on the capacity-%d client", narrow)
			assert.Equal(
				t,
				wide,
				rec.peakFor("wide"),
				"peak in-flight requests on the capacity-%d client", wide)
			assert.Equal(
				t,
				narrow+wide,
				rec.crossClientPeak(),
				"peak in-flight requests across both clients at once; "+
					"a single shared semaphore cannot exceed %d", maxConcurrentRequests)
		})
	}
}

// TestConcurrencyLimiter_ContentionIsReal is the control for the test above.
// A "two clients have independent limiters" assertion passes trivially if
// neither limiter was ever contended, so this proves the instrument can see
// contention at all: one client, capacity 1, more requests than capacity, and
// a peak of 1 while 4 would be available to an unlimited client.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestConcurrencyLimiter_ContentionIsReal() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	const requestCount = 8

	// target is unreachable on purpose: with capacity 1 the recorder can
	// never see 2 in flight, so every handler holds for the full (short)
	// hold window, which is the slowest, most contention-prone shape this
	// client can be put under.
	rec := newOverlapRecorder(2, 20*time.Millisecond)
	srv := rec.server(t, "only")

	limited, _ := KiotaHTTPClient(count.New(), ConcurrencyLimit(1))
	hammer(ctx, t, limited, srv.URL, requestCount)

	require.Equal(t, 1, rec.peakFor("only"), "capacity-1 client peak in-flight")

	// And the same shape without the limiter in the way reaches more than 1,
	// so the assertion above is a measurement, not a property of the harness.
	unlimited := &http.Client{}
	unlimitedRec := newOverlapRecorder(requestCount, 2*time.Second)
	unlimitedSrv := unlimitedRec.server(t, "only")

	hammer(ctx, t, unlimited, unlimitedSrv.URL, requestCount)

	assert.Greater(
		t,
		unlimitedRec.peakFor("only"),
		1,
		"a client with no limiter reaches more than one request in flight")
}

// ---------------------------------------------------------------------------
// criterion 3: a limiter is always installed
// ---------------------------------------------------------------------------

// TestKiotaMiddlewares_AlwaysInstallsOneConcurrencyLimiter pins the "cannot
// arise by construction" half of the old nil-singleton failure: there is no
// option set, and no clientConfig, that produces a kiota middleware chain
// without exactly one usable concurrency limiter in it.
//
// The old code reached a no-limiter chain through the public
// api.InitConcurrencyLimit with a non-Exchange service, which nilled the
// singleton; kiotaMiddlewares then skipped the middleware entirely. Neither
// the singleton nor that skip exists any more, so the chain is checked
// directly.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestKiotaMiddlewares_AlwaysInstallsOneConcurrencyLimiter() {
	table := []struct {
		name      string
		cc        *clientConfig
		expectCap int
	}{
		{
			name:      "no options at all",
			cc:        populateConfig(),
			expectCap: maxConcurrentRequests,
		},
		{
			name:      "zero value clientConfig, bypassing populateConfig",
			cc:        &clientConfig{},
			expectCap: maxConcurrentRequests,
		},
		{
			name:      "unrelated options only",
			cc:        populateConfig(NoTimeout(), MaxRetries(2)),
			expectCap: maxConcurrentRequests,
		},
		{
			name:      "explicit narrow limit",
			cc:        populateConfig(ConcurrencyLimit(1)),
			expectCap: 1,
		},
		{
			name:      "explicit out of range limit",
			cc:        populateConfig(ConcurrencyLimit(9001)),
			expectCap: maxConcurrentRequests,
		},
		{
			name:      "later option wins",
			cc:        populateConfig(ConcurrencyLimit(1), ConcurrencyLimit(3)),
			expectCap: 3,
		},
	}

	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			clientOptions := msgraphsdkgo.GetDefaultClientOptions()
			mw := kiotaMiddlewares(&clientOptions, test.cc, count.New())

			limiters := []*concurrencyLimiterMiddleware{}

			for _, m := range mw {
				if cl, ok := m.(*concurrencyLimiterMiddleware); ok {
					limiters = append(limiters, cl)
				}
			}

			require.Len(t, limiters, 1, "concurrency limiters in the middleware chain")
			require.NotNil(t, limiters[0].semaphore, "the installed limiter's semaphore")
			assert.Equal(t, test.expectCap, cap(limiters[0].semaphore), "installed semaphore capacity")
		})
	}
}

// TestConcurrencyLimiter_EveryClientIsLimited is the behavioural half of the
// criterion above: a client built with no options at all is still bounded,
// which is what the old nil singleton broke.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestConcurrencyLimiter_EveryClientIsLimited() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	const requestCount = 12

	rec := newOverlapRecorder(maxConcurrentRequests, 2*time.Second)
	srv := rec.server(t, "default")

	defaulted, cc := KiotaHTTPClient(count.New())
	assert.Equal(t, maxConcurrentRequests, cc.concurrencyLimit, "default client concurrency limit")

	hammer(ctx, t, defaulted, srv.URL, requestCount)

	assert.Equal(
		t,
		maxConcurrentRequests,
		rec.peakFor("default"),
		"an option-less client is bounded at maxConcurrentRequests, not unbounded")
}

// TestPopulateConfig_LastOptionWins pins the ordering property that
// api.NewClient's option forwarding relies on, in the one package where
// clientConfig's fields are visible.
//
// api.newLargeItemService forwards its caller's whole graph.Option slice and
// appends NoTimeout() last, so a caller cannot take the "no timeout" away
// from the large-item client - that being the entire reason that client
// exists - while still having its other options (retries, backoff, an
// explicit ConcurrencyLimit) apply to both of an api.Client's services. If
// options ever stopped being applied in order, or NoTimeout stopped being
// appended last, a caller-supplied Timeout would silently cap large-item
// downloads at an hour.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestPopulateConfig_LastOptionWins() {
	t := suite.T()

	// NoTimeout last: it wins, which is what newLargeItemService needs.
	assert.Equal(
		t,
		defaultNoTimeout,
		populateConfig(Timeout(time.Second), NoTimeout()).timeout,
		"NoTimeout appended after a caller's Timeout")

	// And the converse, so the assertion above is about ordering rather than
	// about NoTimeout being unconditionally special.
	assert.Equal(
		t,
		time.Second,
		populateConfig(NoTimeout(), Timeout(time.Second)).timeout,
		"a Timeout applied after NoTimeout")

	// The same ordering is what lets api.NewClient prepend its derived
	// concurrency limit and still let an explicit option override it.
	assert.Equal(
		t,
		2,
		populateConfig(ConcurrencyLimit(4), ConcurrencyLimit(2)).concurrencyLimit,
		"a later ConcurrencyLimit overrides an earlier one")
}

// ---------------------------------------------------------------------------
// criterion 2: first-caller-wins cannot come back
// ---------------------------------------------------------------------------

// TestGraphPackage_HasNoPackageLevelConcurrencyLimiterState proves the
// structural claim behind "first caller wins is gone": the capacity cannot be
// latched by the first caller in the process because the package holds no
// limiter and no latch to latch.
//
// It parses this package's own non-test sources and fails on any package-level
// declaration that mentions concurrencyLimiterMiddleware or sync.Once. Both
// are specific to the bug being fixed: the limiter used to live in a
// package-level var (concurrencyLimitMiddlewareSingleton) guarded by a
// package-level sync.Once, so the first InitializeConcurrencyLimiter call in
// the process decided the capacity, and the enable=false branch nilled the
// var outright.
//
// What this check cannot see: state reached through another package, state
// inside a function (a closure over a captured variable), or a latch built
// from something other than sync.Once - a bool plus a mutex, or
// sync/atomic. It is a guard against the specific pattern this chunk removed
// reappearing by habit, not a proof that no global state can exist. The
// behavioural tests above are what prove the capacity is per client.
func (suite *PerClientConcurrencyLimiterUnitSuite) TestGraphPackage_HasNoPackageLevelConcurrencyLimiterState() {
	t := suite.T()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	checked := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		require.NoError(t, err, "parsing %s", name)

		checked++

		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}

			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				var found []string

				ast.Inspect(vs, func(n ast.Node) bool {
					switch typed := n.(type) {
					case *ast.Ident:
						if typed.Name == "concurrencyLimiterMiddleware" {
							found = append(found, "concurrencyLimiterMiddleware")
						}
					case *ast.SelectorExpr:
						if pkg, ok := typed.X.(*ast.Ident); ok &&
							pkg.Name == "sync" && typed.Sel.Name == "Once" {
							found = append(found, "sync.Once")
						}
					}

					return true
				})

				names := []string{}
				for _, id := range vs.Names {
					names = append(names, id.Name)
				}

				assert.Empty(
					t,
					found,
					"%s declares package-level var %s referencing %v; "+
						"the Graph concurrency limiter is per client and must not be "+
						"latched or stored at package scope",
					name,
					strings.Join(names, ", "),
					found)
			}
		}
	}

	require.Greater(t, checked, 0, "parsed source files in this package")
}
