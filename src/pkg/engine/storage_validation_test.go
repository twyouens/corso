package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
)

// TestValidateStorageCredentials proves chunk 0.6's review fix: an S3
// Storage with no access key and/or secret key is rejected before any
// network call, rather than falling through to Kopia's own AWS SDK
// ambient-credential chain (env vars, then IMDS). Filesystem storage has
// no credential concept and is never rejected by this check.
func TestValidateStorageCredentials(t *testing.T) {
	newS3 := func(accessKey, secretKey string) storage.Storage {
		st, err := storage.NewStorage(
			storage.ProviderS3,
			&storage.S3Config{
				AWS:      credentials.AWS{AccessKey: accessKey, SecretKey: secretKey},
				Bucket:   "bucket",
				Endpoint: "127.0.0.1:1", // closed port: a broken check would fail fast, not hang
			},
			storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: "passphrase"}})
		require.NoError(t, err)

		return st
	}

	table := []struct {
		name      string
		st        storage.Storage
		expectErr bool
	}{
		{
			name:      "both keys present",
			st:        newS3("access-key", "secret-key"),
			expectErr: false,
		},
		{
			name:      "empty access key",
			st:        newS3("", "secret-key"),
			expectErr: true,
		},
		{
			name:      "empty secret key",
			st:        newS3("access-key", ""),
			expectErr: true,
		},
		{
			name:      "both empty",
			st:        newS3("", ""),
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
