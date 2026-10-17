package kopia

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/alcionai/clues"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/s3"

	"github.com/twyouens/corso/src/pkg/control/repository"
	"github.com/twyouens/corso/src/pkg/storage"
)

const (
	defaultS3Endpoint = "s3.amazonaws.com" // matches kopia's default value

	// s3TLSHandshakeTimeout bounds a slow or hanging TLS handshake when
	// opening an S3-backed repository connection. Alcion's Kopia fork carried
	// this as a field on s3.Options (set unconditionally to 60s); upstream
	// Kopia has no such field (its getCustomTransport only handles
	// DoNotVerifyTLS and RootCA), so it is reapplied here instead of carrying
	// a forked dependency. See withS3TLSHandshakeTimeout.
	s3TLSHandshakeTimeout = 60 * time.Second
)

// s3TransportMu serializes the brief global-default-transport swap performed
// by withS3TLSHandshakeTimeout so concurrent repository opens don't race each
// other over http.DefaultTransport.
var s3TransportMu sync.Mutex

// withS3TLSHandshakeTimeout runs fn with the process-wide http.DefaultTransport
// temporarily replaced by a clone that has TLSHandshakeTimeout set to
// s3TLSHandshakeTimeout, then restores the original transport before
// returning.
//
// This exists only because upstream Kopia's S3 storage (repo/blob/s3) builds
// its own *http.Transport internally, by cloning http.DefaultTransport, with
// no way for a caller to supply a custom transport or timeout (section 6 of
// docs/kopia-fork-audit.md). Swapping the package-level default for the
// duration of the call is the only available extension point short of
// carrying a patch to the vendored dependency. The swap is synchronous,
// mutex-guarded against other callers of this function, and restored via
// defer on every return path (including panics), but it is a genuine,
// documented widening of scope versus the fork's version: the fork's field
// only ever touched S3 connections; this widens the 60s handshake timeout to
// any HTTP call elsewhere in the process that happens to run, using the
// unmodified http.DefaultTransport, during the (short, non-networked in the
// common case) window this function holds it swapped. The risk is judged
// acceptable because the only change is a longer handshake allowance (10s ->
// 60s), never a shorter one, so no caller can newly time out because of it.
func withS3TLSHandshakeTimeout(fn func() (blob.Storage, error)) (blob.Storage, error) {
	s3TransportMu.Lock()
	defer s3TransportMu.Unlock()

	original := http.DefaultTransport

	defer func() {
		http.DefaultTransport = original
	}()

	originalTransport, ok := original.(*http.Transport)
	if !ok {
		// Stdlib default is always *http.Transport; if something upstream of
		// us ever replaces it with another RoundTripper implementation we
		// have no safe way to clone it, so leave it untouched and skip
		// reapplying the timeout rather than guessing.
		return fn()
	}

	withTimeout := originalTransport.Clone()
	withTimeout.TLSHandshakeTimeout = s3TLSHandshakeTimeout
	http.DefaultTransport = withTimeout

	return fn()
}

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

	store, err := withS3TLSHandshakeTimeout(func() (blob.Storage, error) {
		return s3.New(ctx, &opts, false)
	})
	if err != nil {
		return nil, clues.StackWC(ctx, err)
	}

	return store, nil
}
