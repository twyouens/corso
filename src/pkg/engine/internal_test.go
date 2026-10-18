package engine

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/version"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/count"
)

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

	tags := map[string]string{
		"k3ep-tenant":       "t1",
		model.ServiceTag:    "exchange",
		model.BackupTypeTag: model.MergeBackup,
	}

	b := &backup.Backup{
		BaseModel: model.BaseModel{
			ID:           model.StableID(uuid.NewString()),
			ModelStoreID: manifest.ID(uuid.NewString()),
			Tags:         tags,
		},
		CreationTime: now,
		Version:      version.Backup,
		ErrorCount:   2,
	}
	b.BytesUploaded = 1234
	b.ItemsRead = 7

	bp := toBackupPoint(b)

	assert.Equal(t, string(b.ID), bp.ID)
	assert.Equal(t, BackupPointTypeMerge, bp.Type)
	assert.Equal(t, b.Tags, bp.Tags)
	assert.Equal(t, now, bp.CreatedAt)
	assert.EqualValues(t, 7, bp.ItemsRead)
	assert.EqualValues(t, 1234, bp.BytesUploaded)
	assert.Equal(t, 2, bp.ErrorCount)

	// the returned Tags must be an independent copy: mutating it must not
	// reach back into the Backup model.
	bp.Tags["k3ep-tenant"] = "mutated"
	assert.Equal(t, "t1", b.Tags["k3ep-tenant"])
}

// TestBackupPointTypeFrom proves BackupPointType correctly classifies
// every backup-type tag value the fork's internal/model package uses
// (merge, assist, preview), plus the edge cases
// pkg/backup.Backup.Type() itself documents: a genuine pre-v8 backup
// with no recorded type at all (a positive Version, defaults to merge),
// and any other/missing value (unknown) - so k3ep never has to hardcode
// internal/model's string literals itself to tell these apart (see
// BackupPointType's own doc comment for why that matters). Also proves
// chunk 0.6's review round-4 fix: an untagged model with a zero
// (unset) Version is Unknown, not Merge, even though
// pkg/backup.Backup.Type() itself would default it to merge-backup -
// Version 0 is never a genuine pre-v8 backup, only a model some other
// tool or a legacy/partial write left with no Version recorded.
func TestBackupPointTypeFrom(t *testing.T) {
	table := []struct {
		name   string
		b      *backup.Backup
		expect BackupPointType
	}{
		{
			name: "merge",
			b: &backup.Backup{BaseModel: model.BaseModel{
				Tags: map[string]string{model.BackupTypeTag: model.MergeBackup},
			}},
			expect: BackupPointTypeMerge,
		},
		{
			name: "assist",
			b: &backup.Backup{BaseModel: model.BaseModel{
				Tags: map[string]string{model.BackupTypeTag: model.AssistBackup},
			}},
			expect: BackupPointTypeAssist,
		},
		{
			name: "preview",
			b: &backup.Backup{BaseModel: model.BaseModel{
				Tags: map[string]string{model.BackupTypeTag: model.PreviewBackup},
			}},
			expect: BackupPointTypePreview,
		},
		{
			name: "no tag, pre-v8 backup defaults to merge (pkg/backup.Backup.Type()'s own rule)",
			b: &backup.Backup{
				BaseModel: model.BaseModel{Tags: map[string]string{}},
				Version:   1,
			},
			expect: BackupPointTypeMerge,
		},
		{
			name: "no tag, post-v8 backup is unknown",
			b: &backup.Backup{
				BaseModel: model.BaseModel{Tags: map[string]string{}},
				Version:   version.Backup,
			},
			expect: BackupPointTypeUnknown,
		},
		{
			name: "no tag, zero (unset) version is unknown, not a false merge",
			b: &backup.Backup{
				BaseModel: model.BaseModel{Tags: map[string]string{}},
				Version:   0,
			},
			expect: BackupPointTypeUnknown,
		},
		{
			name: "no tag, negative non-NoBackup version is unknown",
			b: &backup.Backup{
				BaseModel: model.BaseModel{Tags: map[string]string{}},
				Version:   -2,
			},
			expect: BackupPointTypeUnknown,
		},
		{
			name: "unrecognized tag value is unknown",
			b: &backup.Backup{BaseModel: model.BaseModel{
				Tags: map[string]string{model.BackupTypeTag: "something-new"},
			}},
			expect: BackupPointTypeUnknown,
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expect, backupPointTypeFrom(test.b))
		})
	}
}

// TestBackupPointType_IsIndependentlyRestorable proves the optional
// round-4 predicate added so a caller never has to re-derive "only merge
// is restorable" from this type's own doc comments: merge is the only
// restorable value, every other defined value and the type's zero value
// are not.
func TestBackupPointType_IsIndependentlyRestorable(t *testing.T) {
	table := []struct {
		name   string
		typ    BackupPointType
		expect bool
	}{
		{"merge is restorable", BackupPointTypeMerge, true},
		{"assist is not restorable", BackupPointTypeAssist, false},
		{"preview is not restorable", BackupPointTypePreview, false},
		{"unknown is not restorable", BackupPointTypeUnknown, false},
		{"zero value is not restorable", BackupPointType(""), false},
		{"an unrecognized future value is not restorable", BackupPointType("something-new"), false},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expect, test.typ.IsIndependentlyRestorable())
		})
	}
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

// TestErrEmptyTagFilter_MessageNamesNoSingleCaller pins the wording of
// an error two different operations return. Chunk 0.7 made
// FindItemsByName return ErrEmptyTagFilter as well as DeleteBackupPoints,
// and corrected the doc comment but not the message string, so a caller
// reading a log saw "DeleteBackupPoints requires at least one tag" after
// calling FindItemsByName - which is actively misleading when the log
// line is the only evidence of what happened. Chunk 0.9 reworded it; this
// test fails if a future caller is named in it again without the others.
//
// The error's identity is unchanged: errors.Is still recognises it from
// both callers, which TestDeleteBackupPoints_RejectsEmptyTags and
// browse_test.go's TestFindItemsByName_RequiresTagsAndName assert against
// the real methods. This test re-asserts it for both in one place, so
// the pairing is visible.
func TestErrEmptyTagFilter_MessageNamesNoSingleCaller(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	msg := ErrEmptyTagFilter.Error()

	namesDelete := strings.Contains(msg, "DeleteBackupPoints")
	namesFind := strings.Contains(msg, "FindItemsByName")

	assert.Equal(t, namesDelete, namesFind,
		"this error is returned by DeleteBackupPoints and FindItemsByName alike: "+
			"its message must name both callers or neither, never one")

	r := &Repository{}

	_, err := r.DeleteBackupPoints(ctx, nil)
	assert.ErrorIs(t, err, ErrEmptyTagFilter, "DeleteBackupPoints must still return it")

	_, err = r.FindItemsByName(ctx, FindItemsRequest{Name: "anything"})
	assert.ErrorIs(t, err, ErrEmptyTagFilter, "FindItemsByName must still return it")
}

// TestDeleteBackupPoints_RejectsEmptyTags proves DeleteBackupPoints never
// touches the underlying repository at all (r.repo stays nil and unused)
// when called with no tags - the guard against "delete everything" fires
// before anything else happens.
func TestDeleteBackupPoints_RejectsEmptyTags(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r := &Repository{}

	ids, err := r.DeleteBackupPoints(ctx, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyTagFilter)
	assert.Empty(t, ids)

	ids, err = r.DeleteBackupPoints(ctx, map[string]string{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyTagFilter)
	assert.Empty(t, ids)
}
