package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
)

// TestRepositoryConfig_NeverLeaksCredentials proves a RepositoryConfig
// (and its Storage and Account fields, alone or embedded several layers
// down - exactly the "a job payload persisted to a queue" shape chunk
// 1.4's job queue creates) never leaks the repository's own encryption
// key or the Microsoft 365 application's client secret, through any of
// five channels a real caller might actually use:
//
//   - fmt (%v, %+v, %#v, %s, and calling String()/GoString() directly) -
//     the %s/%+v/%v cases via fmt.Stringer, %#v via fmt.GoStringer, both
//     of which this package implements (redact.go).
//   - encoding/json, which consults neither of the above and needed its
//     own MarshalJSON (redact.go here, and pkg/storage's and
//     pkg/account's own redact.go for their own types) - checked for
//     RepositoryConfig directly, for a struct embedding one (the job
//     payload shape), and for Storage/Account marshalled alone, since
//     each must be safe standing by itself too, not only through
//     RepositoryConfig's own override.
//   - log/slog, via LogValue.
//   - go.uber.org/zap, which this fork's own logger (pkg/logger) uses:
//     zap.Any dispatches to a value's fmt.Stringer when present (falling
//     back to reflection only when it is not), so this is actually
//     exercising the same String() this test's fmt cases do, through a
//     different call path - checked against a real zapcore.NewJSONEncoder
//     writing to a buffer, not an encoding-agnostic test double, so the
//     actual dispatch logic runs.
//   - github.com/alcionai/clues, which every error in this fork's own
//     call paths uses: a config value attached via Err.With ends up in
//     ErrCore.Values (map[string]any), checked both through ErrCore's own
//     String()/Format() (fmt-based, so Stringer-safe) and through
//     json.Marshal(ErrCore) (encoding/json on a map[string]any consults
//     each value's own dynamic type, so MarshalJSON-safe).
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

	assertNoSecrets := func(t *testing.T, label, out string) {
		t.Helper()

		for _, secret := range secrets {
			assert.NotContains(t, out, secret, "%s leaked a credential", label)
		}
	}

	t.Run("fmt", func(t *testing.T) {
		//lint:ignore S1025 deliberately exercising the %s verb through fmt.Sprintf, not calling String() directly
		sVerbOutput := fmt.Sprintf("%s", cfg) //nolint:staticcheck // deliberately exercising the %s verb through fmt.Sprintf

		outputs := map[string]string{
			"%v":       fmt.Sprintf("%v", cfg),
			"%+v":      fmt.Sprintf("%+v", cfg),
			"%#v":      fmt.Sprintf("%#v", cfg),
			"%s":       sVerbOutput,
			"String()": cfg.String(),
		}

		for verb, out := range outputs {
			assertNoSecrets(t, "fmt verb "+verb, out)
		}
	})

	t.Run("json", func(t *testing.T) {
		cfgJSON, err := json.Marshal(cfg)
		require.NoError(t, err)
		assertNoSecrets(t, "json.Marshal(cfg)", string(cfgJSON))

		// The shape chunk 1.4's job queue actually creates: a
		// RepositoryConfig embedded several layers down in a payload
		// struct, not marshalled on its own.
		type jobPayload struct {
			JobID string           `json:"job_id"`
			Repo  RepositoryConfig `json:"repo"`
		}

		payloadJSON, err := json.Marshal(jobPayload{JobID: "job-1", Repo: cfg})
		require.NoError(t, err)
		assertNoSecrets(t, "json.Marshal(struct embedding RepositoryConfig)", string(payloadJSON))

		storageJSON, err := json.Marshal(cfg.Storage)
		require.NoError(t, err)
		assertNoSecrets(t, "json.Marshal(cfg.Storage) alone", string(storageJSON))

		accountJSON, err := json.Marshal(cfg.Account)
		require.NoError(t, err)
		assertNoSecrets(t, "json.Marshal(cfg.Account) alone", string(accountJSON))
	})

	t.Run("slog", func(t *testing.T) {
		var buf bytes.Buffer

		handler := slog.NewJSONHandler(&buf, nil)
		logger := slog.New(handler)
		logger.Info("test", slog.Any("config", cfg))

		assertNoSecrets(t, "slog JSON handler", buf.String())
	})

	t.Run("zap", func(t *testing.T) {
		var buf bytes.Buffer

		writer := zapcore.AddSync(&buf)
		encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
		core := zapcore.NewCore(encoder, writer, zapcore.InfoLevel)
		logger := zap.New(core)

		logger.Info("test", zap.Any("config", cfg))
		require.NoError(t, logger.Sync())

		assertNoSecrets(t, "zap JSON encoder", buf.String())
	})

	t.Run("clues", func(t *testing.T) {
		ctx, flush := tester.NewContext(t)
		defer flush()

		err := clues.WithClues(clues.New("boom").With("config", cfg), ctx)
		core := clues.ToCore(err)

		assertNoSecrets(t, "clues ErrCore.String()", core.String())
		assertNoSecrets(t, "fmt %v on clues ErrCore", fmt.Sprintf("%v", core))

		coreJSON, jerr := json.Marshal(core)
		require.NoError(t, jerr)
		assertNoSecrets(t, "json.Marshal(clues.ErrCore)", string(coreJSON))
	})

	// Sanity check: prove this test is not vacuous by confirming the raw,
	// unredacted struct really does carry the secrets somewhere - so if
	// RepositoryConfig ever stopped embedding credentials entirely, this
	// assertion (not the ones above) would be the one that fails and says
	// so.
	t.Run("sanity: the raw struct really does carry the secrets", func(t *testing.T) {
		rawStorage := fmt.Sprintf("%v", cfg.Storage.Config)
		rawAccount := fmt.Sprintf("%v", cfg.Account.Config)
		assert.Contains(t, rawStorage, secretPassphrase)
		assert.Contains(t, rawStorage, secretAccessKey)
		assert.Contains(t, rawStorage, secretSecretKey)
		assert.Contains(t, rawAccount, secretClientSecret)
	})
}
