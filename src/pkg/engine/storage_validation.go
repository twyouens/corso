package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
	"github.com/twyouens/corso/src/pkg/storage"
)

// ErrMissingStorageCredentials is returned by InitializeRepository and
// OpenRepository when cfg.Storage is an S3-compatible configuration with
// a blank (or whitespace-only - " " and "\t" are not valid AWS
// credentials either) access key or secret key.
//
// Kopia's own S3 blob storage, given no static credentials, falls back to
// its AWS SDK's ambient credential chain: environment variables, then the
// EC2/ECS/IMDS instance role. That used to be treated as the right
// behaviour for the fork's CLI, running under a human's or a CI job's
// own AWS identity; chunk 0.9 dropped that, so it is now the wrong
// behaviour everywhere in this fork. It was always wrong for this
// package: a caller-chosen
// bucket, prefix and endpoint silently reaching the *platform's own*
// ambient AWS identity - because a caller forgot an access key, or some
// other part of the config was tenant-influenced - is exactly the kind of
// storage access ENC-4 ("storage providers never see plaintext", and by
// extension never see more than the one tenant's data they were
// explicitly handed) exists to prevent. Proven live: with no caller-chosen
// credentials, a MinIO bucket was reachable using this machine's own
// ambient AWS identity.
//
// pkg/storage.S3Config.validate() (the shared validator) enforces this
// same requirement, for every caller, with no opt-out - chunk 0.6's
// security reviewer proved the condition was reachable even through
// pkg/repository.New + Initialize directly, bypassing this package
// entirely, since pkg/repository is itself public (REPO-2 permits k3ep to
// import it; only prose steered callers to this facade instead). Chunk
// 0.6 first added an opt-out so the fork's CLI could keep connecting
// with no explicit keys; chunk 0.9 deleted that capability outright, on
// the user's decision, so explicit S3 credentials are now required
// everywhere in this fork.
//
// This function remains, as a second, earlier layer: it fails fast,
// before any Microsoft 365 connection is attempted, with this package's
// own named error, rather than failing later (after an M365 round trip)
// with pkg/storage's more generic one. It is deliberately independent of
// whether the shared validator already caught the same problem (see its
// own body): the two may give different feedback, but a caller going
// through this package always sees ErrMissingStorageCredentials for this
// specific condition, never a leftover ambiguity about which layer
// enforced it.
var ErrMissingStorageCredentials = errors.New(
	"engine: S3 storage requires an explicit, non-blank access key and secret key; " +
		"this package never falls back to ambient AWS credentials")

// validateStorageCredentials is a no-op for every storage provider other
// than S3. See ErrMissingStorageCredentials.
func validateStorageCredentials(st storage.Storage) error {
	if st.Provider != storage.ProviderS3 {
		return nil
	}

	// ToS3Config's underlying buildS3ConfigFromMap always returns a
	// non-nil *S3Config populated from whatever was in the config map,
	// even when it also returns a non-nil error (its own validate()
	// call) - so the credential check below runs regardless of err,
	// checked first and on its own terms. This matters now that
	// pkg/storage.S3Config.validate() performs this same check itself:
	// without reading s3Cfg directly here, a caller of this package
	// would see validate()'s own, more generic wrapped error instead of
	// this package's named ErrMissingStorageCredentials for the exact
	// same condition.
	s3Cfg, err := st.ToS3Config()
	if s3Cfg == nil {
		return fmt.Errorf("engine: reading S3 storage config: %w", err)
	}

	if len(strings.TrimSpace(s3Cfg.AccessKey)) == 0 || len(strings.TrimSpace(s3Cfg.SecretKey)) == 0 {
		return ErrMissingStorageCredentials
	}

	// Some other problem the shared validator caught (for example a
	// missing bucket) - surface it as-is; it is not a credentials issue.
	if err != nil {
		return fmt.Errorf("engine: reading S3 storage config: %w", err)
	}

	return nil
}

// ensureConcurrencyLimiterInitialized calls the fork's own
// graph.InitializeConcurrencyLimiter with a value this package controls,
// before repository.New ever gets a chance to connect a Microsoft 365
// data provider and make that call itself with a service this package
// cannot yet know (see InitializeRepository's and OpenRepository's doc
// comments: both connect a provider for path.UnknownService, since no
// backup has chosen a real service yet).
//
// graph.InitializeConcurrencyLimiter is a sync.Once: whichever of this
// function's call or m365.NewController's internal call runs first, for
// the lifetime of the process, decides whether every Exchange Graph call
// afterwards gets the item-fetch concurrency limiter at all.
// m365.NewController passes enable = (service == path.ExchangeService),
// so path.UnknownService - and so, without this function, every
// InitializeRepository/OpenRepository call - would permanently disable
// the limiter for the whole process the moment it ran first, silently
// removing Exchange's only concurrency guard for every later backup this
// process ever runs, Exchange or not, k3ep's long-lived-worker shape
// included. Calling this first, always with enable=true, means the
// decision this package controls is "the limiter exists", which is
// correct for Exchange.
//
// This is not a free or neutral choice for every other service, and an
// earlier version of this comment called it "harmless" and "inert" -
// measured and found false. concurrencyLimiterMiddleware is a single,
// process-global semaphore, shared across every service and every
// concurrent backup this process runs, not a per-client or per-tenant
// one; it is not configurable per call once latched, since it is exactly
// the same sync.Once this function is racing to win correctly. Measured
// against 24 concurrent plain Users().GetByID-shaped Graph calls: ~400ms
// with the limiter forced off, ~745ms clamped to 4 in-flight requests,
// ~1.647s clamped to 1. A worker process backing up many tenants
// concurrently pays this clamp on every service, all the time, as the
// cost of this function's fix for Exchange - and the first repository
// ever opened in the process is what decides the clamp's width for every
// later one, including an open that goes on to fail Microsoft 365
// authentication entirely. The user has approved a separate, later chunk
// to replace this process-global singleton with a per-client limiter;
// this function deliberately does not attempt that here, and nothing in
// it should be read as implying the current cost is acceptable long
// term, only accurately described.
//
// capacity falls back to control.DefaultOptions()'s value if opts.Parallelism.ItemFetch
// is not positive: a capacity of 0 would create a semaphore every request
// blocks on forever, and callers who only override other control.Options
// fields and never touch Parallelism (including every example and test in
// this fork's history prior to this check existing) must not be handed
// exactly that by a zero-value control.Options{}.
func ensureConcurrencyLimiterInitialized(ctx context.Context, opts control.Options) {
	capacity := opts.Parallelism.ItemFetch
	if capacity <= 0 {
		capacity = control.DefaultOptions().Parallelism.ItemFetch
	}

	graph.InitializeConcurrencyLimiter(ctx, true, capacity)
}
