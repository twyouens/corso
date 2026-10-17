package kopia

import (
	"context"

	"github.com/alcionai/clues"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/s3"

	"github.com/twyouens/corso/src/pkg/control/repository"
	"github.com/twyouens/corso/src/pkg/storage"
)

const (
	defaultS3Endpoint = "s3.amazonaws.com" // matches kopia's default value
)

func s3BlobStorage(
	ctx context.Context,
	repoOpts repository.Options,
	s storage.Storage,
) (blob.Storage, error) {
	cfg, err := s.ToS3Config()
	if err != nil {
		return nil, clues.StackWC(ctx, err)
	}

	endpoint := defaultS3Endpoint
	if len(cfg.Endpoint) > 0 {
		endpoint = cfg.Endpoint
	}

	opts := s3.Options{
		BucketName:      cfg.Bucket,
		Endpoint:        endpoint,
		Prefix:          cfg.Prefix,
		DoNotUseTLS:     cfg.DoNotUseTLS,
		DoNotVerifyTLS:  cfg.DoNotVerifyTLS,
		AccessKeyID:     cfg.AccessKey,
		SecretAccessKey: cfg.SecretKey,
		SessionToken:    cfg.SessionToken,
		PointInTime:     repoOpts.ViewTimestamp,
	}

	// Alcion's Kopia fork set TLSHandshakeTimeout: 60 (seconds) here,
	// unconditionally, on every S3 connection. Upstream Kopia's s3.New has no
	// injection point for a custom transport or timeout: getCustomTransport
	// (repo/blob/s3/s3_storage.go) hard-clones http.DefaultTransport and only
	// ever customizes it for DoNotVerifyTLS or RootCA, never a handshake
	// timeout.
	//
	// An earlier version of this code tried to work around that by briefly
	// swapping the package-level http.DefaultTransport for the duration of
	// this call. That is unsafe: http.DefaultTransport is a plain
	// (unsynchronized) interface variable, and this fork already has another,
	// unrelated reader of it with no shared lock
	// (pkg/services/m365/api/graph/http_wrapper.go's defaultTransport(),
	// reached whenever Corso builds a Graph HTTP client). A worker opening an
	// S3 repository while another goroutine builds a Graph client races on
	// that variable -- confirmed under `go run -race`, which reports it
	// immediately and repeatably as a data race on the interface value
	// itself (a torn read/write), independent of what the two sides store in
	// it. k3ep runs exactly this pattern in production (Graph calls and S3
	// repository opens in the same worker process), so this was not a
	// theoretical risk.
	//
	// A per-process sync.Once write would remove the race (a single
	// happens-before write instead of a repeated swap) but is not equivalent
	// behavior either: it would permanently loosen the TLS handshake timeout
	// for every HTTP client in the process, including Graph's, where the
	// fork's original only ever affected S3 connections. Silently changing
	// an unrelated client's timeout is worse than losing the bound.
	//
	// So: dropped. S3-backed repository connections now use Go's
	// unmodified default TLS handshake timeout (10 seconds,
	// http.DefaultTransport's built-in value) instead of the fork's 60. If a
	// real STO-1 destination is ever found to need longer than that, the
	// correct fix is an upstream contribution adding a transport or timeout
	// option to Kopia's S3 storage, not a local global mutation.
	store, err := s3.New(ctx, &opts, false)
	if err != nil {
		return nil, clues.StackWC(ctx, err)
	}

	return store, nil
}
