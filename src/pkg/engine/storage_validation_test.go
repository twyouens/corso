package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
)

// TestValidateStorageCredentials proves chunk 0.6's review fix: an S3
// Storage with a blank or whitespace-only access key and/or secret key is
// rejected before any network call, rather than falling through to
// Kopia's own AWS SDK ambient-credential chain (env vars, then IMDS).
// Filesystem storage has no credential concept and is never rejected by
// this check.
//
// The bad-credential cases are built as raw Storage{Config: map[string]string{...}}
// literals, not through storage.NewStorage - pkg/storage.S3Config's own
// shared validator rejects the same blank credentials unconditionally
// (see its validate()), so storage.NewStorage itself refuses to
// construct a Storage with blank S3 credentials at all. A raw literal is
// how a config could still reach this function with blank credentials -
// built by hand instead of through the public constructor - and this
// test is specifically about this package's own, independent layer:
// whatever ToS3Config() returns, this function always surfaces its own
// named ErrMissingStorageCredentials for this exact condition, not
// validate()'s more generic one.
func TestValidateStorageCredentials(t *testing.T) {
	rawS3 := func(accessKey, secretKey string) storage.Storage {
		return storage.Storage{
			Provider: storage.ProviderS3,
			Config: map[string]string{
				"s3_bucket":              "bucket",
				"s3_endpoint":            "127.0.0.1:1", // closed port: a broken check would fail fast, not hang
				"s3_access_key":          accessKey,
				"s3_secret_key":          secretKey,
				"common_corsoPassphrase": "passphrase",
			},
		}
	}

	table := []struct {
		name      string
		st        storage.Storage
		expectErr bool
	}{
		{
			name:      "empty access key",
			st:        rawS3("", "secret-key"),
			expectErr: true,
		},
		{
			name:      "empty secret key",
			st:        rawS3("access-key", ""),
			expectErr: true,
		},
		{
			name:      "both empty",
			st:        rawS3("", ""),
			expectErr: true,
		},
		{
			name:      "whitespace-only access key",
			st:        rawS3("   ", "secret-key"),
			expectErr: true,
		},
		{
			name:      "whitespace-only secret key (tab)",
			st:        rawS3("access-key", "\t"),
			expectErr: true,
		},
		{
			name: "filesystem storage is never rejected, even with no credential concept",
			st: func() storage.Storage {
				st, err := storage.NewStorage(
					storage.ProviderFilesystem,
					&storage.FilesystemConfig{Path: t.TempDir()},
					storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: "passphrase"}})
				require.NoError(t, err)

				return st
			}(),
			expectErr: false,
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			err := validateStorageCredentials(test.st)

			if test.expectErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrMissingStorageCredentials)

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestValidateStorageCredentials_RealCredentialsPassBothLayers proves the
// happy path still works end to end through the public constructor now
// that pkg/storage.S3Config.validate() also enforces this requirement:
// storage.NewStorage must still succeed, and this package's own check
// must still pass, for an S3 Storage built with real-looking credentials
// the ordinary way.
func TestValidateStorageCredentials_RealCredentialsPassBothLayers(t *testing.T) {
	st, err := storage.NewStorage(
		storage.ProviderS3,
		&storage.S3Config{
			AWS:      credentials.AWS{AccessKey: "access-key", SecretKey: "secret-key"},
			Bucket:   "bucket",
			Endpoint: "127.0.0.1:1",
		},
		storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: "passphrase"}})
	require.NoError(t, err)

	assert.NoError(t, validateStorageCredentials(st))
}
