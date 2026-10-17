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

// ErrReservedTagKey is returned by Backup when a BackupRequest.Tags key
// collides with a tag key the fork's own bookkeeping already uses
// (currently "service" and "backup-type"). Those two tags drive
// merge/assist/preview base-selection logic for every backup in the
// repository; letting a caller's tag override one of them, even by
// accident, could silently corrupt that logic or - the adversarial case -
// let a caller cross tenant boundaries by forging a tag that is supposed to
// be reserved for the fork alone.
var ErrReservedTagKey = errors.New("engine: tag key is reserved by the backup engine")

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
	// tenant, policy and object tags. This package stores and filters by
	// tags; it never interprets their values. A key that collides with
	// the fork's own reserved tag keys ("service", "backup-type") is
	// rejected with ErrReservedTagKey before any backup work starts.
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
	// Tags are exactly the tags this backup was created with (via
	// BackupRequest.Tags), plus the fork's own "service" and
	// "backup-type" bookkeeping tags.
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
// pairs in tags. A nil or empty tags returns every backup point in the
// repository.
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
// every one supplied. It returns the number of backup points deleted.
//
// tags must be non-empty: see ErrEmptyTagFilter. This is a deliberate
// guard rail, not a limitation worked around elsewhere - there is no way
// to delete "everything" through this function, by design.
func (r *Repository) DeleteBackupPoints(ctx context.Context, tags map[string]string) (int, error) {
	if len(tags) == 0 {
		return 0, ErrEmptyTagFilter
	}

	bs, err := r.backupsByTag(ctx, tags)
	if err != nil {
		return 0, err
	}

	if len(bs) == 0 {
		return 0, nil
	}

	ids := make([]string, 0, len(bs))

	for _, b := range bs {
		ids = append(ids, string(b.ID))
	}

	if err := r.repo.DeleteBackups(ctx, true, ids...); err != nil {
		return 0, fmt.Errorf("engine: deleting backup points: %w", err)
	}

	return len(ids), nil
}

func (r *Repository) backupsByTag(ctx context.Context, tags map[string]string) ([]*backup.Backup, error) {
	var opts []store.FilterOption

	if len(tags) > 0 {
		opts = append(opts, store.Tags(tags))
	}

	bs, err := r.repo.BackupsByTag(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("engine: listing backup points: %w", err)
	}

	return bs, nil
}

// validateTags rejects a caller's tag map outright if any key collides
// with a tag key the fork's own bookkeeping reserves for itself. See
// ErrReservedTagKey; operations.mergeAdditionalTags inside the fork
// enforces this too, defensively, for any caller of
// BackupOperation.AdditionalTags that bypasses this check, but this is the
// one every caller going through this package actually hits, with a clear
// error up front instead of a silently dropped tag.
func validateTags(tags map[string]string) error {
	for _, reserved := range []string{model.ServiceTag, model.BackupTypeTag} {
		if _, ok := tags[reserved]; ok {
			return fmt.Errorf("%w: %q", ErrReservedTagKey, reserved)
		}
	}

	return nil
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
