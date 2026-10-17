package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/count"
)

// TestValidateTags_RejectsReservedKeys proves a caller can never create a
// backup tagged with the fork's own reserved bookkeeping keys ("service",
// "backup-type") through this package - this is the first of the two
// layers described in BackupRequest.Tags' doc comment; the second lives in
// internal/operations.mergeAdditionalTags.
func TestValidateTags_RejectsReservedKeys(t *testing.T) {
	table := []struct {
		name      string
		tags      map[string]string
		expectErr bool
	}{
		{
			name:      "nil tags is fine",
			tags:      nil,
			expectErr: false,
		},
		{
			name:      "ordinary caller tags are fine",
			tags:      map[string]string{"tenant": "t1", "policy": "p1", "object": "o1"},
			expectErr: false,
		},
		{
			name:      "colliding with service is rejected",
			tags:      map[string]string{model.ServiceTag: "forged"},
			expectErr: true,
		},
		{
			name:      "colliding with backup-type is rejected",
			tags:      map[string]string{model.BackupTypeTag: "forged"},
			expectErr: true,
		},
		{
			name:      "one reserved key among several others is still rejected",
			tags:      map[string]string{"tenant": "t1", model.ServiceTag: "forged"},
			expectErr: true,
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			err := validateTags(test.tags)

			if test.expectErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrReservedTagKey)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestCloneTags(t *testing.T) {
	assert.Nil(t, cloneTags(nil))

	in := map[string]string{"tenant": "t1"}
	out := cloneTags(in)
	assert.Equal(t, in, out)

	// mutating the copy must not affect the original, and vice versa.
	out["tenant"] = "mutated"
	assert.Equal(t, "t1", in["tenant"])

	in["tenant"] = "mutated-again"
	assert.Equal(t, "mutated", out["tenant"])
}

func TestToBackupPoint(t *testing.T) {
	now := time.Now().UTC()

	b := &backup.Backup{
		BaseModel: model.BaseModel{
			ID:           model.StableID(uuid.NewString()),
			ModelStoreID: manifest.ID(uuid.NewString()),
			Tags:         map[string]string{"tenant": "t1", model.ServiceTag: "exchange"},
		},
		CreationTime: now,
		ErrorCount:   2,
	}
	b.BytesUploaded = 1234
	b.ItemsRead = 7

	bp := toBackupPoint(b)

	assert.Equal(t, string(b.ID), bp.ID)
	assert.Equal(t, b.Tags, bp.Tags)
	assert.Equal(t, now, bp.CreatedAt)
	assert.EqualValues(t, 7, bp.ItemsRead)
	assert.EqualValues(t, 1234, bp.BytesUploaded)
	assert.Equal(t, 2, bp.ErrorCount)

	// the returned Tags must be an independent copy: mutating it must not
	// reach back into the Backup model.
	bp.Tags["tenant"] = "mutated"
	assert.Equal(t, "t1", b.Tags["tenant"])
}

func TestDeltaCounts(t *testing.T) {
	baseline := map[string]int64{"items-added": 5, "persisted-uploaded-bytes": 100}
	current := map[string]int64{"items-added": 12, "persisted-uploaded-bytes": 250, "new-key": 3}

	got := deltaCounts(baseline, current)

	assert.Equal(t, map[string]int64{"items-added": 7, "persisted-uploaded-bytes": 150, "new-key": 3}, got)
}

func TestStartProgress_NilIsNoop(t *testing.T) {
	stop := startProgress(count.New(), nil)
	require.NotNil(t, stop)

	// must return promptly and not panic.
	stop()
}

// TestStartProgress_PollsAndFinalizes proves the progress callback is
// invoked at least once while work is "in progress" (simulated here by
// holding the goroutine open past one tick) and exactly once more, last,
// with Done true - and that by the time the returned stop function
// returns, no further call will happen.
func TestStartProgress_PollsAndFinalizes(t *testing.T) {
	bus := count.New()
	bus.Add("items-added", 1)

	var (
		mu          sync.Mutex
		snapshots   []ProgressSnapshot
		calledAfter bool
	)

	stop := startProgress(bus, func(s ProgressSnapshot) {
		mu.Lock()
		defer mu.Unlock()

		snapshots = append(snapshots, s)
	})

	// Give the ticker time to fire at least once before we add more work
	// and stop.
	time.Sleep(3 * progressInterval)

	bus.Add("items-added", 4)

	stop()

	mu.Lock()
	defer mu.Unlock()

	calledAfter = len(snapshots) > 0

	require.True(t, calledAfter, "progress callback must have been invoked")

	last := snapshots[len(snapshots)-1]
	assert.True(t, last.Done, "the final call must have Done set")
	// baseline was 1 (added before startProgress), total is now 1+4=5, so
	// the call-scoped delta this backup "did" is 4.
	assert.EqualValues(t, 4, last.Counts["items-added"])

	// every call before the last must not have Done set.
	for _, s := range snapshots[:len(snapshots)-1] {
		assert.False(t, s.Done)
	}

	// no call happens after stop() returns: sleep past another interval and
	// confirm the snapshot count is unchanged.
	countAfterStop := len(snapshots)
	time.Sleep(2 * progressInterval)
	assert.Equal(t, countAfterStop, len(snapshots))
}

// TestDeleteBackupPoints_RejectsEmptyTags proves DeleteBackupPoints never
// touches the underlying repository at all (r.repo stays nil and unused)
// when called with no tags - the guard against "delete everything" fires
// before anything else happens.
func TestDeleteBackupPoints_RejectsEmptyTags(t *testing.T) {
	r := &Repository{}

	n, err := r.DeleteBackupPoints(nil, nil) //nolint:staticcheck // nil ctx is fine, never used on this path
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyTagFilter)
	assert.Equal(t, 0, n)

	n, err = r.DeleteBackupPoints(nil, map[string]string{}) //nolint:staticcheck
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyTagFilter)
	assert.Equal(t, 0, n)
}
