package kopia

import (
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kopia/kopia/snapshot/upload"
	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/backup/details"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/fault"
)

// TestCorsoProgress_ConcurrentCallbacks drives FinishedFile and
// FinishedHashingFile from several goroutines at once, the way Kopia's
// uploader calls them during a backup.
//
// Its point is the -race run. Both callbacks update corsoProgress's
// totalFiles/totalBytes counters with atomic adds, and FinishedFile logs
// the two counters every 1000 files. If that log line (or the check that
// gates it) reads either counter with a plain load, `go test -race`
// reports a data race against the other goroutines' atomic adds. The
// file count is chosen so the logging branch is reached more than once;
// without it, the read of totalBytes inside that branch is never
// exercised. Without -race the test still checks that no update is lost.
func TestCorsoProgress_ConcurrentCallbacks(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	cp := &corsoProgress{
		ctx:      ctx,
		Progress: &upload.NullUploadProgress{},
		deets:    &details.Builder{},
		pending:  map[string]*itemDetails{},
		errs:     fault.New(true),
		counter:  count.New(),
	}

	const (
		goroutines   = 8
		perGoroutine = 500
		total        = goroutines * perGoroutine
		bytesPerFile = 3
	)

	// FinishedHashingFile base64-decodes each path segment and logs the
	// third onward; valid segments keep the run free of decode-failure
	// log lines.
	seg := base64.StdEncoding.EncodeToString([]byte("seg"))
	hashedName := strings.Join([]string{seg, seg, seg}, "/")

	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)

		go func(g int) {
			defer wg.Done()

			for i := 0; i < perGoroutine; i++ {
				// Not in cp.pending, so FinishedFile counts it and returns
				// before touching details.
				cp.FinishedFile("not-pending-"+strconv.Itoa(g)+"-"+strconv.Itoa(i), nil)
				cp.FinishedHashingFile(hashedName, bytesPerFile)
			}
		}(g)
	}

	wg.Wait()

	assert.Equal(t, int64(total), atomic.LoadInt64(&cp.totalFiles), "files counted")
	assert.Equal(t, int64(total*bytesPerFile), atomic.LoadInt64(&cp.totalBytes), "bytes counted")
	assert.Equal(t, int64(total*bytesPerFile), cp.counter.Get(count.PersistedHashedBytes), "hashed bytes")
}
