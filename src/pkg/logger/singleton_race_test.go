package logger

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// resetSingleton clears the lazily built process logger so a test can
// drive its first initialisation, and restores the previous state when the
// test ends. ResolvedLogFile is pinned to stderr meanwhile, so the lazy
// default never creates a log file under the user's own log directory.
func resetSingleton(t *testing.T) {
	singletonMu.Lock()
	defer singletonMu.Unlock()

	prevLogger, prevCore, prevFile := loggerton, logCore, ResolvedLogFile
	loggerton, logCore = nil, nil
	ResolvedLogFile = Stderr

	t.Cleanup(func() {
		singletonMu.Lock()
		defer singletonMu.Unlock()

		loggerton, logCore, ResolvedLogFile = prevLogger, prevCore, prevFile
	})
}

// TestSingleton_ConcurrentFirstUse makes the process logger's first use
// happen on several goroutines at once, through both of its entry points:
//   - a lookup on a context that carries no logger (zslFromCtx, behind
//     every logger.Ctx call), which resolves the default settings - what
//     graph's LoggingMiddleware does for concurrent requests made on a
//     background context, and what any library caller that never seeds a
//     logger does;
//   - CtxOrSeed with explicit settings (singleton), which is how
//     tester.NewContext and the cmd/ programs seed one.
//
// Each path takes the lock on its own, so the test drives both: a test
// using only the first leaves singleton's own lock unexercised (removing
// it stayed green until the second path was added).
//
// Without synchronisation, every such caller can see loggerton == nil and
// build its own logger, writing loggerton, logCore, ResolvedLogFile (via
// EnsureDefaults) and clues' process-wide hasher concurrently: a data race
// under -race, and, even without -race, callers handed different loggers.
// The test asserts every caller gets the same one.
func TestSingleton_ConcurrentFirstUse(t *testing.T) {
	resetSingleton(t)

	const callers = 16

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		got   = make([]*zap.SugaredLogger, callers)
	)

	for i := 0; i < callers; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			<-start

			//nolint:forbidigo
			ctx := context.Background()

			if i%2 == 0 {
				got[i] = zslFromCtx(ctx)
				_ = Ctx(ctx)

				return
			}

			// Explicit stderr: never create a log file from a test.
			_, got[i] = CtxOrSeed(ctx, Settings{File: Stderr, Format: LFText, Level: LLInfo})
		}(i)
	}

	close(start)
	wg.Wait()

	for i := range got {
		assert.NotNil(t, got[i], "caller %d got a logger", i)
		assert.Same(t, got[0], got[i], "caller %d got the same process logger as caller 0", i)
	}
}
