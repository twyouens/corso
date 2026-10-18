package engine

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
)

// TestRepositoryConfig_NeverLeaksCredentials proves chunk 0.6's review
// fix: formatting a RepositoryConfig under every fmt verb a caller might
// reasonably use - including the ones that respect fmt.Stringer/
// fmt.GoStringer by default (%v, %+v, %s) and the one that does not
// (%#v, which needs its own GoStringer implementation) - and passing it
// through slog, never includes the repository's own encryption key or
// the Microsoft 365 application's client secret.
func TestRepositoryConfig_NeverLeaksCredentials(t *testing.T) {
	const (
		secretPassphrase   = "super-secret-corso-passphrase-0.6"
		secretClientSecret = "super-secret-azure-client-secret-0.6"
		secretAccessKey    = "super-secret-s3-access-key-0.6"
		secretSecretKey    = "super-secret-s3-secret-key-0.6"
	)

	st, err := storage.NewStorage(
		storage.ProviderS3,
		&storage.S3Config{
			AWS:    credentials.AWS{AccessKey: secretAccessKey, SecretKey: secretSecretKey},
			Bucket: "bucket",
		},
		storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: secretPassphrase}})
	require.NoError(t, err)

	acct, err := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365:          credentials.M365{AzureClientID: "client-id", AzureClientSecret: secretClientSecret},
			AzureTenantID: "tenant-id",
		})
	require.NoError(t, err)

	cfg := RepositoryConfig{Storage: st, Account: acct, Options: control.DefaultOptions()}

	secrets := []string{secretPassphrase, secretClientSecret, secretAccessKey, secretSecretKey}

	outputs := map[string]string{
		"%v":       fmt.Sprintf("%v", cfg),
		"%+v":      fmt.Sprintf("%+v", cfg),
		"%#v":      fmt.Sprintf("%#v", cfg),
		"%s":       fmt.Sprintf("%s", cfg), //nolint:gosimple // deliberately exercising this verb
		"String()": cfg.String(),
	}

	for verb, out := range outputs {
		for _, secret := range secrets {
			assert.NotContains(t, out, secret, "verb %s leaked a credential", verb)
		}
	}

	// slog: LogValue must be consulted, not slog's default reflection-based
	// struct formatting.
	logged := slog.GroupValue(slog.Any("config", cfg)).String()
	for _, secret := range secrets {
		assert.NotContains(t, logged, secret, "slog output leaked a credential")
	}

	// Sanity check: prove this test is not vacuous by confirming the raw,
	// unredacted struct really does carry the secrets somewhere - so if
	// RepositoryConfig ever stopped embedding credentials entirely, this
	// assertion (not the ones above) would be the one that fails and says
	// so.
	rawStorage := fmt.Sprintf("%v", cfg.Storage.Config)
	rawAccount := fmt.Sprintf("%v", cfg.Account.Config)
	assert.Contains(t, rawStorage, secretPassphrase)
	assert.Contains(t, rawStorage, secretAccessKey)
	assert.Contains(t, rawStorage, secretSecretKey)
	assert.Contains(t, rawAccount, secretClientSecret)
}
