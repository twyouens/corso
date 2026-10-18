package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kopia/kopia/repo/manifest"

	"github.com/twyouens/corso/src/internal/model"
)

// Reserved tag keys and prefixes a caller's BackupRequest.Tags can never
// use. See validateTags.
//
//   - model.ServiceTag ("service") and model.BackupTypeTag ("backup-type")
//     drive merge/assist/preview base-selection logic for every backup in
//     the repository (internal/operations/backup.go).
//   - manifest.TypeLabelKey ("type"), reservedStableIDKey ("stableID") and
//     reservedStoreVersionKey ("storeVersion") are rejected outright by
//     Kopia's own ModelStore.Put (internal/kopia/model_store.go's
//     stableIDKey/modelVersionKey, unexported there so repeated here as
//     literals) - but only after a real backup has already uploaded a
//     snapshot and written its details to the streamstore, leaving both
//     orphaned - no model, so no way to find or delete them - until a
//     complete maintenance pass eventually garbage-collects them (up to
//     the GC buffer period, not a quick fix). Rejecting these up front
//     means a caller gets a clear, synchronous error instead of a
//     backup that appears to run and then simply disappears.
//   - reservedTenantKey ("tenant") and reservedServiceCategoryPrefix
//     ("sc-") are read and written by the fork's own backup garbage
//     collector (internal/kopia/cleanup_backups.go's tenantTag/
//     serviceCatTagPrefix, likewise unexported there) to decide which
//     assist bases survive collection for a given tenant and category. A
//     caller tag using either would make this package's own "it never
//     interprets tags" promise false: the caller's tag would silently
//     influence which of its own content a later maintenance run keeps.
//     This is a deliberate per-user decision, not a prefix mandated on
//     every tag: ordinary caller tags (k3ep's "k3ep-tenant",
//     "k3ep-policy", "k3ep-object") are unaffected.
const (
	reservedStableIDKey           = "stableID"
	reservedStoreVersionKey       = "storeVersion"
	reservedTenantKey             = "tenant"
	reservedServiceCategoryPrefix = "sc-"
)

var reservedTagKeys = []string{
	model.ServiceTag,
	model.BackupTypeTag,
	manifest.TypeLabelKey,
	reservedStableIDKey,
	reservedStoreVersionKey,
	reservedTenantKey,
}

// Bounds on a caller's tags, independent of which keys are used. Kopia's
// manifest manager holds every manifest's labels in memory for the
// lifetime of each Find call (and the repository connection generally
// caches recently-seen manifests), so an unbounded number of tags, or
// unbounded key/value sizes, is a real memory cost multiplied across
// every backup point in the repository - not just this one caller's
// problem. k3ep's own real tags (tenant, policy, object IDs) are nowhere
// close to these limits; they exist to bound the worst case, not to
// accommodate a legitimate large use this package knows about.
const (
	maxTagCount      = 32
	maxTagKeyBytes   = 256
	maxTagValueBytes = 1024
)

// ErrReservedTagKey is returned by Backup when a BackupRequest.Tags key
// collides with a tag key the fork's own bookkeeping, Kopia's model store,
// or the fork's backup garbage collector reserves for itself. Those
// reserved keys drive logic this package cannot let a caller influence by
// accident, or - the adversarial case - by deliberately forging a tag
// that is supposed to be reserved. See reservedTagKeys' doc comment above
// for exactly which keys and why.
var ErrReservedTagKey = errors.New("engine: tag key is reserved by the backup engine")

// ErrReservedTagPrefix is returned by Backup when a BackupRequest.Tags key
// starts with a prefix the fork's backup garbage collector reserves for
// itself ("sc-"). See reservedTagKeys' doc comment above.
var ErrReservedTagPrefix = errors.New("engine: tag key uses a prefix reserved by the backup engine")

// ErrTooManyTags is returned by Backup when a BackupRequest.Tags has more
// than maxTagCount entries.
var ErrTooManyTags = errors.New("engine: too many tags")

// ErrTagTooLarge is returned by Backup when a BackupRequest.Tags key or
// value exceeds maxTagKeyBytes/maxTagValueBytes.
var ErrTagTooLarge = errors.New("engine: tag key or value is too large")

// validateTags rejects a caller's tag map outright - before any backup
// work starts - if it is too large, or any individual key/value is too
// large, or any key is reserved (see the package-level vars and consts
// above). operations.mergeAdditionalTags inside the fork enforces the
// reserved-key rule too, defensively, for any caller of
// BackupOperation.AdditionalTags that bypasses this check, but this is
// the one every caller going through this package actually hits, with a
// clear, specific error up front instead of a silently dropped or
// truncated tag.
func validateTags(tags map[string]string) error {
	if len(tags) > maxTagCount {
		return fmt.Errorf("%w: got %d, max %d", ErrTooManyTags, len(tags), maxTagCount)
	}

	for _, reserved := range reservedTagKeys {
		if _, ok := tags[reserved]; ok {
			return fmt.Errorf("%w: %q", ErrReservedTagKey, reserved)
		}
	}

	for k, v := range tags {
		if strings.HasPrefix(k, reservedServiceCategoryPrefix) {
			return fmt.Errorf("%w: %q", ErrReservedTagPrefix, reservedServiceCategoryPrefix)
		}

		if len(k) > maxTagKeyBytes {
			return fmt.Errorf("%w: key %q is %d bytes, max %d", ErrTagTooLarge, k, len(k), maxTagKeyBytes)
		}

		if len(v) > maxTagValueBytes {
			return fmt.Errorf("%w: value for key %q is %d bytes, max %d", ErrTagTooLarge, k, len(v), maxTagValueBytes)
		}
	}

	return nil
}
