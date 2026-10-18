package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
	"github.com/twyouens/corso/src/pkg/storage"
)

// ErrMissingStorageCredentials is returned by InitializeRepository and
// OpenRepository when cfg.Storage is an S3-compatible configuration with
// an empty access key or secret key.
//
// Kopia's own S3 blob storage, given no static credentials, falls back to
// its AWS SDK's ambient credential chain: environment variables, then the
// EC2/ECS/IMDS instance role. That is the right behavior for the fork's
// CLI, which legitimately runs under a human's or a CI job's own AWS
// identity, and is exactly why pkg/storage.S3Config.validate() stays
// permissive about it - this package does not change that shared
// validator, and must not. It is the wrong behavior for this package: a
// caller-chosen bucket, prefix and endpoint silently reaching the
// *platform's own* ambient AWS identity - because a caller forgot an
// access key, or some other part of the config was tenant-influenced - is
// exactly the kind of storage access ENC-4 ("storage providers never see
// plaintext", and by extension never see more than the one tenant's data
// they were explicitly handed) exists to prevent. Proven live: with no
// caller-chosen credentials, a MinIO bucket was reachable using this
// machine's own ambient AWS identity.
//
// This package therefore fails closed: an S3 Storage with no explicit
// AccessKey and SecretKey is rejected before any network call is made,
// every time, with no configuration that opts back into the ambient
// chain. Filesystem storage has no credential concept and is unaffected.
var ErrMissingStorageCredentials = errors.New(
	"engine: S3 storage requires an explicit access key and secret key; " +
		"this package never falls back to ambient AWS credentials")

// validateStorageCredentials is a no-op for every storage provider other
// than S3. See ErrMissingStorageCredentials.
func validateStorageCredentials(st storage.Storage) error {
	if st.Provider != storage.ProviderS3 {
		return nil
	}

	s3Cfg, err := st.ToS3Config()
	if err != nil {
		return fmt.Errorf("engine: reading S3 storage config: %w", err)
	}

	if len(s3Cfg.AccessKey) == 0 || len(s3Cfg.SecretKey) == 0 {
		return ErrMissingStorageCredentials
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
// afterwards gets the 4-way (by default) item-fetch concurrency limiter
// at all. m365.NewController passes enable = (service == path.ExchangeService),
// so path.UnknownService - and so, without this function, every
// InitializeRepository/OpenRepository call - would permanently disable
// the limiter for the whole process the moment it ran first, silently
// removing Exchange's only concurrency guard for every later backup this
// process ever runs, Exchange or not, k3ep's long-lived-worker shape
// included. Calling this first, always with enable=true, means the
// decision this package controls is "the limiter exists", which is
// correct for Exchange and harmless for every other service (an
// unnecessary but otherwise inert concurrency cap).
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
