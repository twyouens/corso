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
// happen on several goroutines at once, each with a context that carries
// no logger - which is what graph's LoggingMiddleware does for concurrent
// requests made on a background context, and what any library caller that
// never seeds a logger does.
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

			got[i] = zslFromCtx(ctx)
			_ = Ctx(ctx)
		}(i)
	}

	close(start)
	wg.Wait()

	for i := range got {
		assert.NotNil(t, got[i], "caller %d got a logger", i)
		assert.Same(t, got[0], got[i], "caller %d got the same process logger as caller 0", i)
	}
}
