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

// TestResolvedLogFile_ConcurrentAccess drives every in-package reader and
// writer of ResolvedLogFile at once, after the process logger is built:
// logger.Ctx on a context with no logger (defaultSingleton), logger.Set,
// Settings.EnsureDefaults, GetLogFile and PreloadLoggingFlags. Each of
// those resolves the default log file, reading ResolvedLogFile and, for
// all but GetLogFile, writing it.
//
// Its point is the -race run. 0.12's first fix locked only the
// defaultSingleton path, so logger.Set - whose EnsureDefaults call wrote
// ResolvedLogFile unlocked - still raced every logger.Ctx on a background
// context (review measured 6 race reports from 8 of each). Every caller
// here must take singletonMu for the variable.
//
// PreloadLoggingFlags runs on one goroutine only: it also binds the
// package's flag-value globals (LogLevelFV and the rest) through addFlags,
// which are CLI start-up state written by cobra too, and are deliberately
// not guarded. Two concurrent calls race on those, not on ResolvedLogFile.
func TestResolvedLogFile_ConcurrentAccess(t *testing.T) {
	resetSingleton(t)

	//nolint:forbidigo
	bg := context.Background()

	// Build the process logger first, so the concurrent phase below
	// exercises ResolvedLogFile's accessors and not the first build
	// (TestSingleton_ConcurrentFirstUse covers that).
	_ = zslFromCtx(bg)

	const perKind = 8

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		run   = func(fn func()) {
			wg.Add(1)

			go func() {
				defer wg.Done()

				<-start
				fn()
			}()
		}
	)

	for i := 0; i < perKind; i++ {
		run(func() { _ = Ctx(bg) })
		run(func() { _ = Set(bg, zap.NewNop().Sugar()) })
		run(func() { _ = Settings{}.EnsureDefaults() })
		run(func() { _ = GetLogFile("") })
	}

	// ResolvedLogFile is pinned to stderr by resetSingleton, and
	// GetLogFile returns it ahead of the flag value, so this creates no
	// file.
	run(func() { _ = PreloadLoggingFlags([]string{"--log-file", "-"}) })

	close(start)
	wg.Wait()

	assert.Equal(t, Stderr, GetLogFile(""), "every caller resolved the same log file")
}
