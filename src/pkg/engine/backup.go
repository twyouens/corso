package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/store"
)

// ErrEmptyTagFilter is returned by DeleteBackupPoints when called with no
// tags. Deleting "every backup point with no particular tag" is never
// implied by an empty filter: a caller that genuinely wants that iterates
// ListBackupPoints(ctx, nil) themselves and deletes each ID explicitly, so
// that intent is visible at the call site rather than hidden behind an
// empty map.
var ErrEmptyTagFilter = errors.New("engine: DeleteBackupPoints requires at least one tag")

// progressInterval is how often a running backup's ProgressFunc is called
// while the backup is in progress, in addition to the one guaranteed final
// call (Done: true) once it finishes.
const progressInterval = 250 * time.Millisecond

// ProgressSnapshot is a point-in-time view of a running backup's progress,
// delivered to a BackupRequest's Progress callback. Counts are cumulative,
// real, server-confirmed counters (see pkg/count's key documentation for
// the full set of keys the fork tracks, e.g. "items-added",
// "persisted-uploaded-bytes") measured from the start of this specific
// Backup call, never a fabricated estimate, and never including an item
// name, subject or any other content.
type ProgressSnapshot struct {
	// Counts maps a pkg/count key to its current value for this backup.
	// Keys present vary by workload and are not guaranteed stable across
	// fork versions; treat an absent key as 0, not as an error.
	Counts map[string]int64
	// Done is true on exactly one call per Backup invocation: the last
	// one, delivered once the backup has finished (successfully or not).
	Done bool
}

// ProgressFunc is invoked periodically while a backup runs, and exactly
// once more, with Done true, when it finishes. It runs on a background
// goroutine that Backup owns and joins before returning, so by the time
// Backup returns no further call will happen; implementations should
// return quickly and must be safe to call repeatedly (never concurrently
// with themselves - calls are always sequential). This is the only
// progress channel this package has: nothing it does writes to stdout.
type ProgressFunc func(ProgressSnapshot)

// BackupRequest describes one backup.
type BackupRequest struct {
	// Selector picks what to back up - which service (Exchange, OneDrive,
	// SharePoint, ...) and which resource owner(s) - built with
	// pkg/selectors. See that package's own documentation and
	// src/cmd/backupexample for a worked example.
	Selector selectors.Selector

	// Tags are stored on the resulting backup point and are later used to
	// list or delete it (ListBackupPoints, DeleteBackupPoints) - k3ep uses
	// its own "k3ep-tenant", "k3ep-policy" and "k3ep-object" keys,
	// deliberately namespaced so they can never collide with a key this
	// package reserves for itself (see tags.go). This package stores and
	// filters by tags; it never interprets their values. Rejected outright,
	// before any backup work starts, and before anything is uploaded: a
	// key that collides with a reserved key or prefix (ErrReservedTagKey,
	// ErrReservedTagPrefix), more than maxTagCount tags (ErrTooManyTags),
	// a key or value over the per-tag byte limits in tags.go
	// (ErrTagTooLarge), or a map whose combined key and value bytes
	// exceed maxTagTotalBytes even though every individual tag is within
	// its own limit (ErrTagsTooLarge).
	Tags map[string]string

	// Progress, if non-nil, is called periodically during the backup and
	// exactly once more at the end - see ProgressFunc.
	Progress ProgressFunc
}

// BackupPoint is everything this package exposes about one completed
// backup, in plain Go types only.
type BackupPoint struct {
	// ID identifies this backup point for later use with
	// DeleteBackupPoints or a future chunk's browse/restore API. Never a
	// secret.
	ID string
	// Type classifies this backup point - see BackupPointType's own doc
	// comment for what each value means and, in particular, why only
	// BackupPointTypeMerge is independently restorable.
	Type BackupPointType
	// Tags are exactly the tags this backup was created with (via
	// BackupRequest.Tags), plus the fork's own "service" and
	// "backup-type" bookkeeping tags. Type above is derived from the
	// "backup-type" entry here (via pkg/backup.Backup.Type(), not by this
	// package re-reading the tag itself); both are provided because a
	// caller may still want the raw string for display or audit
	// purposes, even though Type is what any logic should branch on.
	Tags map[string]string
	// CreatedAt is when this backup point was created.
	CreatedAt time.Time
	// ItemsRead is the number of M365 items read during this backup.
	ItemsRead int64
	// BytesUploaded is the number of bytes this backup point added to the
	// repository (after dedupe/compression).
	BytesUploaded int64
	// ErrorCount is the number of recoverable errors this backup
	// encountered. Zero does not guarantee the backup is complete -
	// Backup itself returns a non-nil error for anything worse.
	ErrorCount int
}

// BackupPointType classifies a BackupPoint by the fork's own internal
// merge/assist/preview distinction, exposed here as a typed, documented
// value specifically so a caller outside this module never has to
// hardcode the literal tag value ("backup-type") or the literal strings
// the fork's internal/model package uses ("merge-backup", "assist-backup",
// "preview-backup") to tell them apart - both of which a k3ep build
// cannot name directly (internal/model is not importable from outside
// this module), so without this type, the only way to interpret
// BackupPoint.Tags["backup-type"] from k3ep would be to hardcode those
// exact literals against a key this very package simultaneously reserves
// callers from setting (see tags.go) - a direct, if accidental, coupling
// to an internal implementation detail REPO-2 exists to prevent. It also
// captures pkg/backup.Backup.Type()'s version-aware default for backups
// older than v8, which never persisted a backup-type tag at all; reading
// BackupPoint.Tags directly would miss that.
type BackupPointType string

const (
	// BackupPointTypeMerge is a complete backup: the only type this
	// package guarantees is independently restorable. A future restore
	// chunk should treat any other type as not restorable on its own.
	BackupPointTypeMerge BackupPointType = "merge"
	// BackupPointTypeAssist is a partial result from kopia-assisted
	// incremental backup - typically left behind by a recoverable error
	// partway through a real backup (see ListBackupPoints' doc comment).
	// Not independently restorable; useful as a base for a future
	// backup's own incremental logic, not as a restore target.
	BackupPointTypeAssist BackupPointType = "assist"
	// BackupPointTypePreview is a deliberately partial backup (see
	// control.Options.PreviewLimits). Not independently restorable in
	// the same sense as a merge backup - it was never meant to be a
	// complete copy of the source data.
	BackupPointTypePreview BackupPointType = "preview"
	// BackupPointTypeUnknown covers any value the fork's own
	// pkg/backup.Backup.Type() returns that this package does not
	// recognize - including, deliberately, the empty string Type()
	// itself documents returning for some pre-v8 backups with no
	// recorded type at all. Treat it as "do not assume restorable."
	BackupPointTypeUnknown BackupPointType = "unknown"
)

// backupPointTypeFrom maps pkg/backup.Backup.Type()'s string result
// (itself derived from internal/model's tag constants, which this
// package may reference internally - it is in the same Go module - but
// never exposes) to the typed, exported BackupPointType above.
func backupPointTypeFrom(b *backup.Backup) BackupPointType {
	switch b.Type() {
	case model.MergeBackup:
		return BackupPointTypeMerge
	case model.AssistBackup:
		return BackupPointTypeAssist
	case model.PreviewBackup:
		return BackupPointTypePreview
	default:
		return BackupPointTypeUnknown
	}
}

// Backup runs one backup and returns the resulting BackupPoint once it
// completes. It connects to Microsoft 365 using the Account the repository
// was opened with (see InitializeRepository/OpenRepository) and reports
// progress through req.Progress, never to stdout.
func (r *Repository) Backup(ctx context.Context, req BackupRequest) (BackupPoint, error) {
	if err := validateTags(req.Tags); err != nil {
		return BackupPoint{}, err
	}

	op, err := r.repo.NewBackupWithLookup(ctx, req.Selector, nil)
	if err != nil {
		return BackupPoint{}, fmt.Errorf("engine: preparing backup: %w", err)
	}

	op.AdditionalTags = cloneTags(req.Tags)

	stopProgress := startProgress(r.counter, req.Progress)

	runErr := op.Run(ctx)

	stopProgress()

	if runErr != nil {
		return BackupPoint{}, fmt.Errorf("engine: running backup: %w", runErr)
	}

	b, err := r.repo.Backup(ctx, string(op.Results.BackupID))
	if err != nil {
		return BackupPoint{}, fmt.Errorf("engine: reading backup result: %w", err)
	}

	return toBackupPoint(b), nil
}

// ListBackupPoints returns every backup point carrying all the key/value
// pairs in tags - a nil or empty tags returns every backup point in the
// repository, across every tenant if the repository ever holds more than
// one (see ErrEmptyTagFilter's and RepositoryConfig's doc comments: this
// package has no notion of "my" tenant, and tags are not an authorization
// boundary).
//
// This includes every backup point that exists, regardless of the fork's
// own internal merge/assist/preview classification - deliberately, and
// unlike the fork's CLI, which has never displayed an assist base
// specifically (the only type pkg/repository's own BackupsByTag ever
// filtered; a preview base was never hidden by that method and so was
// always CLI-visible too - an earlier version of this comment claimed
// otherwise for preview, incorrectly). From this package's point of view
// a backup point is "real" the moment Backup's caller sees its tags
// (BackupPoint.Tags carries whatever the fork classified it as, in the
// "backup-type" key, same as any other tag), whether or not the fork's
// own base-selection logic later folds it into another backup: a
// recoverable error partway through a real backup is enough to leave one
// tagged assist, still genuinely yours, still genuinely occupying
// storage, and this method is how a caller finds out it is there. A
// future browse/restore chunk should keep this in mind: not every listed
// point is necessarily a complete, independently restorable backup the
// way a merge base is.
func (r *Repository) ListBackupPoints(ctx context.Context, tags map[string]string) ([]BackupPoint, error) {
	bs, err := r.backupsByTag(ctx, tags)
	if err != nil {
		return nil, err
	}

	points := make([]BackupPoint, 0, len(bs))

	for _, b := range bs {
		points = append(points, toBackupPoint(b))
	}

	return points, nil
}

// DeleteBackupPoints deletes every backup point carrying all the key/value
// pairs in tags, and only those - never a point whose tags don't match
// every one supplied, and never silently skipping one that does: see
// ListBackupPoints' doc comment for why this also reaches an assist base
// a caller might otherwise never learn exists, let alone delete. It
// returns the IDs of every backup point actually deleted, so a caller can
// record exactly what happened (k3ep's AUD-1 audit event) without a
// separate, racy list-then-delete round trip.
//
// tags must be non-empty: see ErrEmptyTagFilter. This is a deliberate
// guard rail, not a limitation worked around elsewhere - there is no way
// to delete "everything" through this function, by design.
func (r *Repository) DeleteBackupPoints(ctx context.Context, tags map[string]string) ([]string, error) {
	if len(tags) == 0 {
		return nil, ErrEmptyTagFilter
	}

	bs, err := r.backupsByTag(ctx, tags)
	if err != nil {
		return nil, err
	}

	if len(bs) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(bs))

	for _, b := range bs {
		ids = append(ids, string(b.ID))
	}

	if err := r.repo.DeleteBackups(ctx, true, ids...); err != nil {
		return nil, fmt.Errorf("engine: deleting backup points: %w", err)
	}

	return ids, nil
}

// backupsByTag lists through AllBackupsByTag, not pkg/repository's
// BackupsByTag: the latter silently drops any backup tagged
// model.AssistBackup, which hid real, caller-tagged content from both
// ListBackupPoints and DeleteBackupPoints before this was found and
// fixed in review. See AllBackupsByTag's own doc comment in
// pkg/repository/backups.go for exactly how an assist base gets created
// and why that makes it this package's content too, not an internal
// implementation detail safe to filter out.
func (r *Repository) backupsByTag(ctx context.Context, tags map[string]string) ([]*backup.Backup, error) {
	var opts []store.FilterOption

	if len(tags) > 0 {
		opts = append(opts, store.Tags(tags))
	}

	bs, err := r.repo.AllBackupsByTag(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("engine: listing backup points: %w", err)
	}

	return bs, nil
}

func cloneTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}

	cp := make(map[string]string, len(tags))

	for k, v := range tags {
		cp[k] = v
	}

	return cp
}

func toBackupPoint(b *backup.Backup) BackupPoint {
	return BackupPoint{
		ID:            string(b.ID),
		Type:          backupPointTypeFrom(b),
		Tags:          cloneTags(b.Tags),
		CreatedAt:     b.CreationTime,
		ItemsRead:     int64(b.ItemsRead),
		BytesUploaded: b.BytesUploaded,
		ErrorCount:    b.ErrorCount,
	}
}

// startProgress launches the background goroutine that polls counter and
// calls progress periodically, baselined against counter's values at the
// moment this is called so repeated Backup calls on the same *Repository
// (which share one underlying count.Bus) each report progress from zero,
// not from the previous call's cumulative total. It returns a function
// that stops the goroutine, delivers the final Done: true call, and does
// not return until that goroutine has exited - so by the time Backup
// returns, progress is guaranteed to have been called with Done: true and
// nothing further will call progress afterwards.
//
// If progress is nil, startProgress does nothing and returns a no-op stop
// function: this package never polls or allocates for a caller that didn't
// ask for progress reporting.
func startProgress(counter *count.Bus, progress ProgressFunc) (stop func()) {
	if progress == nil {
		return func() {}
	}

	baseline := counter.Values()
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})

	go func() {
		defer close(doneCh)

		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				progress(ProgressSnapshot{Counts: deltaCounts(baseline, counter.Values()), Done: true})
				return
			case <-ticker.C:
				progress(ProgressSnapshot{Counts: deltaCounts(baseline, counter.Values())})
			}
		}
	}()

	return func() {
		close(stopCh)
		<-doneCh
	}
}

// deltaCounts returns current's values minus baseline's, per key, so a
// progress snapshot reflects only the work done since this specific
// Backup call started rather than count.Bus's repository-lifetime total.
// A key present in baseline but missing from current (never happens in
// practice; count.Bus keys only ever increase) is treated as 0.
func deltaCounts(baseline, current map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(current))

	for k, v := range current {
		out[k] = v - baseline[k]
	}

	return out
}
