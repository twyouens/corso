package repository

import (
	"context"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
	storeTD "github.com/twyouens/corso/src/pkg/storage/testdata"
)

// forgedOptOutKeySpellings are the eight spellings of the removed
// opt-out's config-map key that chunk 0.6's security reviewer tried
// against the merged head: snake, camel, upper, mixed, unprefixed, the
// Go field name, and the snake spelling with leading and with trailing
// whitespace. The capability they refer to no longer exists in any form,
// so none of them can mean anything - which is exactly what these tests
// assert, for each spelling individually, so that a re-addition under
// any one of them fails here.
//
// These strings, and the comments explaining them, are the only place in
// src/ that still names the removed capability. That is deliberate: a
// test of a forged key is worthless unless it forges the key an attacker
// would actually send.
var forgedOptOutKeySpellings = []string{
	"s3_allow_ambient_credentials",
	"s3AllowAmbientCredentials",
	"S3_ALLOW_AMBIENT_CREDENTIALS",
	"S3_Allow_Ambient_Credentials",
	"allow_ambient_credentials",
	"AllowAmbientCredentials",
	" s3_allow_ambient_credentials",
	"s3_allow_ambient_credentials ",
}

// setProcessCredentials puts credentials in the process environment -
// the first link in the AWS SDK's own credential chain, and the one the
// security reviewer used to prove the hole was real. They are fake, and
// they are never supplied to any S3Config: the whole point is that
// nothing in this fork may pick them up on a caller's behalf.
func setProcessCredentials(t *testing.T) {
	t.Setenv(credentials.AWSAccessKeyID, "AKIAAMBIENTTESTKEY00")
	t.Setenv(credentials.AWSSecretAccessKey, "ambient-test-secret-key-not-a-real-credential")
	t.Setenv(credentials.AWSSessionToken, "")
}

// credentialLessStorage hand-builds the exported Storage shape - the one
// a caller outside this module can construct without going through
// storage.NewStorage, and so the one that bypasses every constructor-side
// check - with a bucket, the recorder's endpoint, no credentials, and one
// forged opt-out key.
func credentialLessStorage(t *testing.T, endpoint, forgedKey string) storage.Storage {
	st := storage.Storage{
		Provider: storage.ProviderS3,
		Config: map[string]string{
			"s3_bucket":              "credential-less-bucket",
			"s3_endpoint":            endpoint,
			"s3_donotusetls":         "true",
			"common_corsoPassphrase": "0.9-credential-less-passphrase",
			"common_kopiaCfgDir":     t.TempDir(),
		},
	}

	if len(forgedKey) > 0 {
		st.Config[forgedKey] = "true"
	}

	return st
}

// fakeM365Account is a syntactically complete but entirely fake M365
// account. It is here so GetAccountConfigHash succeeds (a local
// computation, no network call) and the storage half of
// GenerateHashForRepositoryConfigFileName is the thing under test. No
// test in this file ever reaches Microsoft 365.
func fakeM365Account(t *testing.T) account.Account {
	acct, err := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365: credentials.M365{
				AzureClientID:     "00000000-0000-0000-0000-000000000000",
				AzureClientSecret: "not-a-real-secret",
			},
			AzureTenantID: "00000000-0000-0000-0000-000000000000",
		})
	require.NoError(t, err, clues.ToCore(err))

	return acct
}

// TestS3CredentialLessConfigNeverReachesAnEndpoint is chunk 0.9's
// criterion-3 reproduction of chunk 0.6's security review attack,
// through pkg/repository - the public, importable route that bypasses
// pkg/engine's own guard entirely, and the route the reviewer used to
// walk round the first fix.
//
// The attack: put credentials in the process environment, point a
// credential-less S3 config at an endpoint you control, and drive it
// through pkg/repository.New + Initialize. At chunk 0.6's round-3 head
// that produced 13 requests signed with the platform's own identity.
// Here it must produce nothing: no request at all, and no Authorization
// header ever emitted.
//
// What each subtest proves, precisely, because the difference matters:
//
//   - "initialize" is the route the brief names. With no usable account
//     it fails at ConnectDataProvider, which Initialize calls before
//     setupKopia ever touches storage, so on its own it would prove only
//     that this route fails - not where. It is here because it is the
//     caller-visible route, and it asserts the recorder stays silent.
//   - "config filename hash" and "update password" are the subtests that
//     bite. Both reach the storage layer with no Microsoft 365 call at
//     all: setupKopia derives the kopia config filename from
//     Storage.GetStorageConfigHash() before connecting, and
//     UpdatePassword does the same before kopia.NewConn().Connect().
//     Remove validate()'s credential requirement and UpdatePassword
//     walks straight on to the recorder's endpoint, signing requests
//     with the environment credentials - which is the mutation this test
//     was checked against.
func TestS3CredentialLessConfigNeverReachesAnEndpoint(t *testing.T) {
	setProcessCredentials(t)

	rec := storeTD.NewS3RequestRecorder(t)

	for _, spelling := range append([]string{""}, forgedOptOutKeySpellings...) {
		name := spelling
		if len(name) == 0 {
			name = "no forged key at all"
		}

		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := credentialLessStorage(t, rec.Endpoint, spelling)

			// the storage layer refuses to derive an S3 config at all
			_, err := st.ToS3Config()
			require.Error(t, err, "a credential-less config map must not produce an S3Config")
			assert.Contains(t, err.Error(), "accessKey")

			t.Run("initialize", func(t *testing.T) {
				r, err := New(ctx, account.Account{}, st, control.DefaultOptions(), NewRepoID)
				require.NoError(t, err, clues.ToCore(err))

				err = r.Initialize(ctx, InitConfig{})
				require.Error(t, err, "a credential-less config must not initialize a repository")
			})

			t.Run("config filename hash", func(t *testing.T) {
				r, err := New(ctx, fakeM365Account(t), st, control.DefaultOptions(), NewRepoID)
				require.NoError(t, err, clues.ToCore(err))

				_, err = r.GenerateHashForRepositoryConfigFileName()
				require.Error(t, err, "the connect path's config filename must not be derivable")
				assert.Contains(t, err.Error(), "accessKey")
			})

			t.Run("update password", func(t *testing.T) {
				r, err := New(ctx, fakeM365Account(t), st, control.DefaultOptions(), NewRepoID)
				require.NoError(t, err, clues.ToCore(err))

				err = r.UpdatePassword(ctx, "a-new-passphrase")
				require.Error(t, err, "a credential-less config must not reach kopia's S3 client")
			})
		})
	}

	// the assertion that actually matters, measured from the endpoint's
	// own side rather than from the code under test
	assert.Empty(t, rec.Requests(), "no request may reach a caller-named S3 endpoint")
	assert.False(t, rec.SawAuthorization(), "nothing may sign a request with any credential")
}
