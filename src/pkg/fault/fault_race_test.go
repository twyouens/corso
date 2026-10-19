package fault_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/fault"
)

// TestBus_FailureReadDuringConcurrentAdds reads Failure() on a fail-fast
// bus, and on its root, while other goroutines add recoverable errors -
// the shape of every collection's "stop spawning workers once the local
// bus has failed" loop (for example exchange's prefetchCollection), whose
// workers report errors to the same local bus the loop keeps checking.
//
// Its point is the -race run. On a fail-fast bus the first AddRecoverable
// promotes its error to the failure, writing e.failure (and the root's)
// under e.mu; if Failure() reads that field without taking e.mu,
// `go test -race` reports a data race here. Without -race the test still
// checks that the promoted failure is one of the errors that was added.
func TestBus_FailureReadDuringConcurrentAdds(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	const workers = 16

	var (
		root  = fault.New(true)
		local = root.Local()
		added = map[string]bool{}
		wg    sync.WaitGroup
		done  = make(chan struct{})
	)

	for i := 0; i < workers; i++ {
		added["worker error "+strconv.Itoa(i)] = true
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			local.AddRecoverable(ctx, clues.New("worker error "+strconv.Itoa(i)))
		}(i)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}

		_ = local.Failure()
		_ = root.Failure()
	}

	if assert.Error(t, local.Failure(), "local bus failure after fail-fast adds") {
		assert.True(t, added[local.Failure().Error()], "local failure is one of the added errors")
	}

	if assert.Error(t, root.Failure(), "root bus failure after fail-fast adds") {
		assert.True(t, added[root.Failure().Error()], "root failure is one of the added errors")
	}
}
