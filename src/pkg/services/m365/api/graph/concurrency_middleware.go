package graph

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/alcionai/clues"
	khttp "github.com/microsoft/kiota-http-go"

	"github.com/twyouens/corso/src/internal/common/limiters"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/logger"
	"github.com/twyouens/corso/src/pkg/path"
)

// ---------------------------------------------------------------------------
// Concurrency Limiter
// "how many calls at one time"
// ---------------------------------------------------------------------------

// concurrencyLimiterMiddleware limits the number of Graph requests that one
// http client has in flight at the same time.
//
// One instance belongs to exactly one client: kiotaMiddlewares builds a fresh
// one for every client it assembles, using that client's own clientConfig
// (see clientConfig.concurrencyLimit and the ConcurrencyLimit option). There
// is deliberately no package-level limiter and no sync.Once latch - the
// capacity is a property of the client, derived from the options its caller
// passed, so two repositories opened in one process with different
// parallelism each get their own semaphore instead of sharing whichever one
// the first caller in the process happened to create.
//
// TestGraphPackage_HasNoPackageLevelConcurrencyLimiterState enforces the
// "no package-level state" half of that mechanically.
type concurrencyLimiterMiddleware struct {
	semaphore chan struct{}
}

// maxConcurrentRequests is the highest per-client capacity this package will
// build a limiter with.  It is also the capacity a client gets when its caller
// asks for something outside the supported range, including the zero value -
// see clampConcurrencyLimit.
const maxConcurrentRequests = 4

// clampConcurrencyLimit folds an arbitrary caller-supplied capacity into the
// range this package supports.  A capacity below 1 would build a semaphore
// every request blocks on forever; a capacity above maxConcurrentRequests
// would exceed what Graph tolerates from one client.  Both become
// maxConcurrentRequests.
func clampConcurrencyLimit(capacity int) int {
	if capacity < 1 || capacity > maxConcurrentRequests {
		return maxConcurrentRequests
	}

	return capacity
}

// generateConcurrencyLimiter builds a limiter for a single client.  Callers
// normally reach this through kiotaMiddlewares rather than directly; the
// clamp is applied here as well as in the ConcurrencyLimit option so a direct
// caller cannot produce a limiter nothing can pass through.
func generateConcurrencyLimiter(capacity int) *concurrencyLimiterMiddleware {
	return &concurrencyLimiterMiddleware{
		semaphore: make(chan struct{}, clampConcurrencyLimit(capacity)),
	}
}

func (cl *concurrencyLimiterMiddleware) Intercept(
	pipeline khttp.Pipeline,
	middlewareIndex int,
	req *http.Request,
) (*http.Response, error) {
	if cl == nil || cl.semaphore == nil {
		return nil, clues.New("nil concurrency limiter")
	}

	cl.semaphore <- struct{}{}
	defer func() {
		<-cl.semaphore
	}()

	return pipeline.Next(req, middlewareIndex)
}

//nolint:lll
// ---------------------------------------------------------------------------
// Rate Limiter
// "how many calls in a minute"
// https://learn.microsoft.com/en-us/sharepoint/dev/general-development/how-to-avoid-getting-throttled-or-blocked-in-sharepoint-online
// ---------------------------------------------------------------------------

const (
	// Default goal is to keep calls below the 10k-per-10-minute threshold.
	// 16 tokens every second nets 960 per minute.  That's 9600 every 10 minutes,
	// which is a bit below the mark.
	// If the bucket is full, we can push out 200 calls immediately, which brings
	// the total in the first 10 minutes to 9800.  We can toe that line if we want,
	// but doing so risks timeouts.  It's better to give the limits breathing room.
	defaultPerSecond = 16  // 16 * 60 * 10 = 9600
	defaultMaxCap    = 200 // real cap is 10k-per-10-minutes

	// Sliding window limiter for exchange service. We are restricting it to 9600 per
	// 10 mins to give the limits some breathing room. It can be slowly increased
	// over time to get closer to the 10k limit.
	exchWindow        = 10 * time.Minute
	exchSlideInterval = 1 * time.Second
	exchCapacity      = 9600

	// since drive runs on a per-minute, rather than per-10-minute bucket, we have
	// to keep the max cap equal to the per-second cap.  A large maxCap pool (say,
	// 1200, similar to the per-minute cap) would allow us to make a flood of 2400
	// calls in the first minute, putting us over the per-minute limit.  Keeping
	// the cap at the per-second burst means we only dole out a max of 1240 in one
	// minute (20 cap + 1200 per minute + one burst of padding).
	drivePerSecond = 20 // 20 * 60 = 1200
	driveMaxCap    = 20 // real cap is 1250-per-minute
)

var (
	driveLimiter = limiters.NewTokenBucketLimiter(drivePerSecond, driveMaxCap)
	// Exchange service token bucket rate limiter
	defaultLimiter = limiters.NewTokenBucketLimiter(defaultPerSecond, defaultMaxCap)

	// Exchange service sliding window rate limiter.
	//
	// TODO(pandeyabs): We are swallowing the error here. It's a limitation of
	// using global limiters. For now, we'll catch any errors in the regression
	// test until we look into managing limiter life cycles.
	exchSlidingLimiter, _ = limiters.NewSlidingWindowLimiter(
		exchWindow,
		exchSlideInterval,
		exchCapacity)
)

type LimiterCfg struct {
	Service path.ServiceType
	// Experimental flag to enable sliding window rate limiter. It should only be
	// enabled for Exchange backups. It's set to false by default to prevent accidental
	// enablement for non backup operations and other services.
	EnableSlidingLimiter bool
}

type limiterCfgKey string

const limiterCfgCtxKey limiterCfgKey = "corsoGaphRateLimiterCfg"

func BindRateLimiterConfig(ctx context.Context, lc LimiterCfg) context.Context {
	return context.WithValue(ctx, limiterCfgCtxKey, lc)
}

func ctxLimiter(ctx context.Context) limiters.Limiter {
	lc, ok := extractRateLimiterConfig(ctx)
	if !ok {
		return defaultLimiter
	}

	lim := defaultLimiter

	switch lc.Service {
	// FIXME: Handle based on category once we add chat backup
	case path.OneDriveService, path.SharePointService, path.GroupsService:
		lim = driveLimiter
	case path.ExchangeService:
		if lc.EnableSlidingLimiter {
			// Return sliding window limiter for Exchange if enabled. Otherwise,
			// return the default token bucket limiter.
			lim = exchSlidingLimiter
		}
	}

	return lim
}

func extractRateLimiterConfig(ctx context.Context) (LimiterCfg, bool) {
	l := ctx.Value(limiterCfgCtxKey)
	if l == nil {
		return LimiterCfg{}, false
	}

	lc, ok := l.(LimiterCfg)

	return lc, ok
}

type limiterConsumptionKey string

const limiterConsumptionCtxKey limiterConsumptionKey = "corsoGraphRateLimiterConsumption"

const (
	// https://learn.microsoft.com/en-us/sharepoint/dev/general-development
	// /how-to-avoid-getting-throttled-or-blocked-in-sharepoint-online#application-throttling
	defaultLC = 1
	// limit consumption rate for single-item GETs requests,
	// or delta-based multi-item GETs, or item content download requests.
	SingleGetOrDeltaLC = 1
	// delta queries without a delta token cost 2 units
	DeltaNoTokenLC = 2
	// limit consumption rate for anything permissions related
	PermissionsLC = 5
)

// ConsumeNTokens ensures any calls using this context will consume
// n rate-limiter tokens.  Default is 1, and this value does not need
// to be established in the context to consume the default tokens.
// This should only get used on a per-call basis, to avoid cross-pollination.
func ConsumeNTokens(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, limiterConsumptionCtxKey, n)
}

func ctxLimiterConsumption(ctx context.Context, defaultConsumption int) int {
	l := ctx.Value(limiterConsumptionCtxKey)
	if l == nil {
		return defaultConsumption
	}

	lc, ok := l.(int)
	if !ok || lc < 1 {
		return defaultConsumption
	}

	return lc
}

// QueueRequest will allow the request to occur immediately if we're under the
// calls-per-minute rate.  Otherwise, the call will wait in a queue until
// the next token set is available.
func QueueRequest(ctx context.Context) {
	limiter := ctxLimiter(ctx)
	consume := ctxLimiterConsumption(ctx, defaultLC)

	if err := limiter.WaitN(ctx, consume); err != nil {
		logger.CtxErr(ctx, err).Error("graph middleware waiting on the limiter")
	}
}

// ResetLimiter resets the limiter to its initial state and refills tokens to
// initial capacity. This is only relevant for the sliding window limiter, and a
// no-op for token bucket limiter. The token bucket limiter doesn't need to be
// reset since it refills tokens at a fixed per-second rate.
func ResetLimiter(ctx context.Context) {
	limiter := ctxLimiter(ctx)
	limiter.Reset()
}

// RateLimiterMiddleware is used to ensure we don't overstep per-min request limits.
type RateLimiterMiddleware struct{}

func (mw *RateLimiterMiddleware) Intercept(
	pipeline khttp.Pipeline,
	middlewareIndex int,
	req *http.Request,
) (*http.Response, error) {
	QueueRequest(getReqCtx(req))
	return pipeline.Next(req, middlewareIndex)
}

// ---------------------------------------------------------------------------
// global throttle fencing
// ---------------------------------------------------------------------------

// timedFence sets up a fence for a certain amount of time.
// the time can be extended arbitrarily.  All processes blocked at
// the fence will be let through when all timer extensions conclude.
type timedFence struct {
	mu     sync.Mutex
	c      chan struct{}
	timers map[int64]*time.Timer
}

func newTimedFence() *timedFence {
	return &timedFence{
		mu:     sync.Mutex{},
		c:      nil,
		timers: map[int64]*time.Timer{},
	}
}

// Block until the fence is let down.
// if no fence is up, return immediately.
// returns if the ctx deadlines before the fence is let down.
func (tf *timedFence) Block(ctx context.Context) error {
	// set to a local var to avoid race panics from tf.c
	// getting set to nil between the conditional check and
	// the read case.  If c gets closed between those two
	// points then the select case will exit immediately,
	// as if we didn't block at all.
	c := tf.c

	if c != nil {
		select {
		case <-ctx.Done():
			return clues.Wrap(ctx.Err(), "blocked on throttling fence")
		case <-c:
		}
	}

	return nil
}

// RaiseFence puts up a fence to block requests for the provided
// duration of time.  Seconds are always added to the current time.
// Multiple calls to RaiseFence are not additive. ie: calling
// `RaiseFence(5); RaiseFence(1)` will keep the fence up until
// now+5 seconds, not now+6 seconds.  When the last remaining fence
// is dropped, all currently blocked calls are allowed through.
func (tf *timedFence) RaiseFence(seconds time.Duration) {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	if seconds < 1 {
		return
	}

	if tf.c == nil {
		tf.c = make(chan struct{})
	}

	timer := time.NewTimer(seconds)
	tid := time.Now().Add(seconds).UnixMilli()
	tf.timers[tid] = timer

	go func(c <-chan time.Time, id int64) {
		// wait for the timeout
		<-c

		tf.mu.Lock()
		defer tf.mu.Unlock()

		// remove the timer
		delete(tf.timers, id)

		// if no timers remain, close the channel to drop the fence
		// and set the fenc channel to nil
		if len(tf.timers) == 0 && tf.c != nil {
			close(tf.c)
			tf.c = nil
		}
	}(timer.C, tid)
}

// throttlingMiddleware is used to ensure we don't overstep per-min request limits.
type throttlingMiddleware struct {
	tf      *timedFence
	counter *count.Bus
}

func (mw *throttlingMiddleware) Intercept(
	pipeline khttp.Pipeline,
	middlewareIndex int,
	req *http.Request,
) (*http.Response, error) {
	err := mw.tf.Block(getReqCtx(req))
	if err != nil {
		return nil, err
	}

	resp, err := pipeline.Next(req, middlewareIndex)
	if resp == nil || err != nil {
		return resp, err
	}

	seconds := getRetryAfterHeader(resp)
	if seconds < 1 {
		return resp, nil
	}

	mw.counter.Inc(count.ThrottledAPICalls)

	// if all prior conditions pass, we need to add a fence that blocks
	// calls, globally, from progressing until the timeout retry-after
	// passes.
	mw.tf.RaiseFence(time.Duration(seconds) * time.Second)

	return resp, nil
}

func getRetryAfterHeader(resp *http.Response) int {
	if resp == nil || len(resp.Header) == 0 {
		return -1
	}

	if resp.StatusCode != http.StatusTooManyRequests {
		return -1
	}

	rah := resp.Header.Get(retryAfterHeader)
	if len(rah) == 0 {
		return -1
	}

	seconds, err := strconv.Atoi(rah)
	if err != nil {
		// the error itself is irrelevant, we only want
		// to wait if we have a clear length of time to wait until.
		return -1
	}

	return seconds
}
