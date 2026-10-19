package graph

import (
	"sync"
	"testing"
	"time"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/tester"
)

// timerCount reads the number of outstanding fence timers under the fence's
// own lock. Tests must use this rather than reading tf.timers directly: each
// timer's goroutine deletes from the map when it fires, so an unlocked read
// from the test goroutine is itself a data race under -race.
func (tf *timedFence) timerCount() int {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	return len(tf.timers)
}

// fenceIsDown reports, under the fence's lock, whether no fence is raised.
func (tf *timedFence) fenceIsDown() bool {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	return tf.c == nil && len(tf.timers) == 0
}

// TestTimedFence_BlockWhileFenceDrops calls Block from several goroutines
// while short fences go up and come down underneath them.
//
// Its point is the -race run. Each fence's timer goroutine sets tf.c to nil
// (under tf.mu) when the fence drops; Block reads tf.c on the caller's
// goroutine. If Block reads tf.c without taking tf.mu, nothing orders that
// read before the timer goroutine's write - the close/receive edge on the
// channel runs the other way - so `go test -race` reports a data race here,
// deterministically, in every round in which a Block caller reaches the
// fence while it is up. Without -race the test still checks that every
// Block returns without error and that the fence ends fully down.
func TestTimedFence_BlockWhileFenceDrops(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	tf := newTimedFence()

	const (
		rounds  = 20
		callers = 4
	)

	for i := 0; i < rounds; i++ {
		tf.RaiseFence(time.Millisecond)

		var wg sync.WaitGroup

		for j := 0; j < callers; j++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				err := tf.Block(ctx)
				assert.NoError(t, err, clues.ToCore(err))
			}()
		}

		wg.Wait()
	}

	require.Eventually(
		t,
		tf.fenceIsDown,
		5*time.Second,
		time.Millisecond,
		"every fence should have dropped once its timer fired")
}
