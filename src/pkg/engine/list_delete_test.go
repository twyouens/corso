package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/repository"
	"github.com/twyouens/corso/src/pkg/store"
)

// fakeRepo implements repository.Repositoryer by embedding it (nil) and
// overriding only the methods ListBackupPoints/DeleteBackupPoints
// actually call. Any other method panics if exercised - these tests
// never need one - which is the point: it keeps this fixture honest
// about exactly what engine.Repository's list/delete path touches,
// without hand-stubbing the fork's much larger Repositoryer interface
// (Restorer, Exporter, Debugger, ...).
type fakeRepo struct {
	repository.Repositoryer

	backups    []*backup.Backup
	deleteErr  error
	deletedIDs []string
}

// AllBackupsByTag deliberately does not re-implement store.Tags'
// filtering logic (already covered by pkg/store's own tests): it exists
// only to prove engine.Repository calls this method - which does not
// drop an assist-tagged backup - rather than BackupsByTag, which does
// (pkg/repository/repository_unexported_test.go proves that distinction
// at the lower layer). Every test using this fixture gives it exactly
// the backups it expects back, tags pre-matched.
func (f *fakeRepo) AllBackupsByTag(ctx context.Context, fs ...store.FilterOption) ([]*backup.Backup, error) {
	return f.backups, nil
}

// BackupsByTag mimics the real pkg/repository behavior this fixture is
// here to contrast against: it drops anything tagged model.AssistBackup.
// Only used by the break-test that proves engine.Repository's
// backupsByTag helper calls AllBackupsByTag, not this.
func (f *fakeRepo) BackupsByTag(ctx context.Context, fs ...store.FilterOption) ([]*backup.Backup, error) {
	out := make([]*backup.Backup, 0, len(f.backups))

	for _, b := range f.backups {
		if b.Tags[model.BackupTypeTag] != model.AssistBackup {
			out = append(out, b)
		}
	}

	return out, nil
}

func (f *fakeRepo) DeleteBackups(ctx context.Context, failOnMissing bool, ids ...string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}

	f.deletedIDs = append(f.deletedIDs, ids...)

	return nil
}

func backupWithTags(tags map[string]string) *backup.Backup {
	b := &backup.Backup{
		BaseModel: model.BaseModel{
			ID:   model.StableID(uuid.NewString()),
			Tags: tags,
		},
		CreationTime: time.Now().UTC(),
	}

	return b
}

// TestListBackupPoints_IncludesAssistBackups proves engine.Repository's
// ListBackupPoints reaches an assist-tagged backup point - one that
// pkg/repository's own, CLI-facing BackupsByTag would have hidden - by
// going through the fixture above rather than AllBackupsByTag's own
// fork-level test (pkg/repository/repository_unexported_test.go), which
// proves the lower layer but not that engine.Repository actually calls
// it.
func TestListBackupPoints_IncludesAssistBackups(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	assist := backupWithTags(map[string]string{model.BackupTypeTag: model.AssistBackup, "k3ep-object": "o1"})
	merge := backupWithTags(map[string]string{model.BackupTypeTag: model.MergeBackup, "k3ep-object": "o1"})

	fr := &fakeRepo{backups: []*backup.Backup{assist, merge}}
	r := &Repository{repo: fr}

	points, err := r.ListBackupPoints(ctx, map[string]string{"k3ep-object": "o1"})
	require.NoError(t, err)

	ids := make([]string, 0, len(points))
	for _, p := range points {
		ids = append(ids, p.ID)
	}

	assert.ElementsMatch(t, []string{string(assist.ID), string(merge.ID)}, ids,
		"ListBackupPoints must include the assist-tagged point, not just the merge one")
}

// TestDeleteBackupPoints_DeletesAssistBackupsAndReturnsIDs proves
// DeleteBackupPoints both reaches an assist-tagged point and returns its
// ID, not just a count a caller could not turn into an AUD-1 audit event
// without a separate, racy list-then-delete round trip.
func TestDeleteBackupPoints_DeletesAssistBackupsAndReturnsIDs(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	assist := backupWithTags(map[string]string{model.BackupTypeTag: model.AssistBackup, "k3ep-object": "o1"})

	fr := &fakeRepo{backups: []*backup.Backup{assist}}
	r := &Repository{repo: fr}

	ids, err := r.DeleteBackupPoints(ctx, map[string]string{"k3ep-object": "o1"})
	require.NoError(t, err)

	assert.Equal(t, []string{string(assist.ID)}, ids)
	assert.Equal(t, []string{string(assist.ID)}, fr.deletedIDs)
}
