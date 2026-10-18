package store

import (
	"context"

	"github.com/alcionai/clues"
	"github.com/kopia/kopia/repo/manifest"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/path"
)

type queryFilters struct {
	tags map[string]string
}

type FilterOption func(*queryFilters)

func (q *queryFilters) populate(qf ...FilterOption) {
	if len(qf) == 0 {
		return
	}

	q.tags = map[string]string{}

	for _, fn := range qf {
		fn(q)
	}
}

// Service ensures the retrieved backups only match
// the specified service.
func Service(pst path.ServiceType) FilterOption {
	return func(qf *queryFilters) {
		qf.tags[model.ServiceTag] = pst.String()
	}
}

// Tags ensures the retrieved backups carry every key/value pair in m, in
// addition to any other FilterOption supplied in the same call (all
// FilterOptions narrow the same underlying tag match; see populate). m is
// copied immediately, at call time, not read lazily when the returned
// FilterOption is later applied: the caller's map stays entirely theirs to
// mutate or discard afterwards with no effect on what this filter matches,
// and the FilterOption value itself is safe to keep and reuse.
//
// This is the general-purpose counterpart to Service above: it accepts any
// caller-defined tag key, including the tenant/policy/object tags
// pkg/engine's callers use. Backups created through pkg/engine.Backup
// cannot have a tag here collide with the fork's own ServiceTag or
// BackupTypeTag - see BackupOperation.AdditionalTags - so combining Tags
// with Service in the same call is safe and meaningful.
func Tags(m map[string]string) FilterOption {
	cp := make(map[string]string, len(m))

	for k, v := range m {
		cp[k] = v
	}

	return func(qf *queryFilters) {
		for k, v := range cp {
			qf.tags[k] = v
		}
	}
}

type (
	BackupWrapper interface {
		BackupGetterDeleter
		GetBackups(
			ctx context.Context,
			filters ...FilterOption,
		) ([]*backup.Backup, error)
	}

	BackupGetterDeleter interface {
		BackupGetter
		BackupDeleter
	}

	BackupGetter interface {
		GetBackup(ctx context.Context, backupID model.StableID) (*backup.Backup, error)
	}

	BackupDeleter interface {
		DeleteBackup(ctx context.Context, backupID model.StableID) error
	}

	ModelDeleter interface {
		DeleteWithModelStoreIDs(ctx context.Context, ids ...manifest.ID) error
	}

	BackupGetterModelDeleter interface {
		BackupGetter
		ModelDeleter
	}

	Storer interface {
		Delete(ctx context.Context, s model.Schema, id model.StableID) error
		Get(ctx context.Context, s model.Schema, id model.StableID, data model.Model) error
		GetIDsForType(ctx context.Context, s model.Schema, tags map[string]string) ([]*model.BaseModel, error)
		GetWithModelStoreID(ctx context.Context, s model.Schema, id manifest.ID, data model.Model) error
		Put(ctx context.Context, s model.Schema, m model.Model) error
		Update(ctx context.Context, s model.Schema, m model.Model) error
		ModelDeleter
	}

	BackupStorer interface {
		Storer
		BackupWrapper
	}
)

type wrapper struct {
	Storer
}

func NewWrapper(s Storer) *wrapper {
	return &wrapper{Storer: s}
}

// GetBackup gets a single backup by id.
func (w wrapper) GetBackup(ctx context.Context, backupID model.StableID) (*backup.Backup, error) {
	b := backup.Backup{}

	err := w.Get(ctx, model.BackupSchema, backupID, &b)
	if err != nil {
		return nil, clues.Wrap(err, "getting backup")
	}

	return &b, nil
}

// GetDetailsFromBackupID retrieves all backups in the model store.
func (w wrapper) GetBackups(
	ctx context.Context,
	filters ...FilterOption,
) ([]*backup.Backup, error) {
	q := &queryFilters{}
	q.populate(filters...)

	bms, err := w.GetIDsForType(ctx, model.BackupSchema, q.tags)
	if err != nil {
		return nil, err
	}

	bs := make([]*backup.Backup, len(bms))

	for i, bm := range bms {
		b := &backup.Backup{}

		err := w.GetWithModelStoreID(ctx, model.BackupSchema, bm.ModelStoreID, b)
		if err != nil {
			return nil, err
		}

		bs[i] = b
	}

	return bs, nil
}

// DeleteBackup deletes the backup and its details entry from the model store.
func (w wrapper) DeleteBackup(ctx context.Context, backupID model.StableID) error {
	return w.Delete(ctx, model.BackupSchema, backupID)
}
