package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/tester/tconfig"
	"github.com/twyouens/corso/src/pkg/credentials"
	storeTD "github.com/twyouens/corso/src/pkg/storage/testdata"
)

// TestNewPrefixedS3Storage_UsesTestConfiguration pins where the gated S3
// integration suites get their storage from: the bucket, endpoint and TLS
// switch from the test configuration (here a CORSO_TEST_CONFIG_FILE), and
// the credentials from the same AWS_* variables those suites are gated on
// (storeTD.AWSStorageCredEnvs) - never from a committed value.
//
// Since chunk 0.6 made S3 credentials mandatory, NewPrefixedS3Storage
// supplied none and so failed at construction in every suite that used it.
//
// It lives here rather than beside pkg/storage/testdata because the go tool
// skips directories named testdata when matching ./..., so a test there
// would never run in `make check`. tconfig.ReadTestConfig caches the first
// configuration it reads for the life of the process; no other test in
// this package reads it, so this test's configuration file is the one read.
func TestNewPrefixedS3Storage_UsesTestConfiguration(t *testing.T) {
	const (
		bucket   = "testdata-bucket"
		endpoint = "127.0.0.1:65535"
		// Obvious fixtures, not credentials.
		accessKey    = "fixture-access-key-id"
		secretKey    = "fixture-secret-access-key"
		sessionToken = "fixture-session-token"
	)

	cfgFile := filepath.Join(t.TempDir(), "corso_test.toml")
	err := os.WriteFile(
		cfgFile,
		[]byte("bucket = '"+bucket+"'\nendpoint = '"+endpoint+"'\ndonotusetls = 'true'\n"),
		0o600)
	require.NoError(t, err, clues.ToCore(err))

	t.Setenv(tconfig.EnvCorsoTestConfigFilePath, cfgFile)
	// The env var takes priority over the file's bucket; keep it out of the way.
	t.Setenv("S3_BUCKET", "")
	t.Setenv(credentials.AWSAccessKeyID, accessKey)
	t.Setenv(credentials.AWSSecretAccessKey, secretKey)
	t.Setenv(credentials.AWSSessionToken, sessionToken)
	// Required by NewStorage's common config, read by GetAndInsertCorso.
	t.Setenv(credentials.CorsoPassphrase, "fixture-passphrase")

	st := storeTD.NewPrefixedS3Storage(t)

	s3Cfg, err := st.ToS3Config()
	require.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, bucket, s3Cfg.Bucket, "bucket from the test configuration")
	assert.Equal(t, endpoint, s3Cfg.Endpoint, "endpoint from the test configuration")
	assert.True(t, s3Cfg.DoNotUseTLS, "TLS switch from the test configuration")
	assert.Equal(t, accessKey, s3Cfg.AccessKey, "access key from "+credentials.AWSAccessKeyID)
	assert.Equal(t, secretKey, s3Cfg.SecretKey, "secret key from "+credentials.AWSSecretAccessKey)
	assert.Equal(t, sessionToken, s3Cfg.SessionToken, "session token from "+credentials.AWSSessionToken)
	assert.Contains(t, s3Cfg.Prefix, t.Name(), "per-test prefix")
}

// TestAWSStorageCredEnvs_SessionTokenIsOptional pins the set of variables
// the gated S3 suites require. Now that tester.MustGetEnvVars checks
// values, every variable in this set must be non-empty for a suite to run,
// and a session token exists only for temporary credentials: static keys,
// MinIO and most S3-compatible stores have none.
func TestAWSStorageCredEnvs_SessionTokenIsOptional(t *testing.T) {
	assert.ElementsMatch(
		t,
		[]string{credentials.AWSAccessKeyID, credentials.AWSSecretAccessKey},
		storeTD.AWSStorageCredEnvs)
}
