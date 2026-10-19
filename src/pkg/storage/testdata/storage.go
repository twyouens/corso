package testdata

import (
	"os"
	"path/filepath"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/common/str"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/tester/tconfig"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
)

const testRepoRootPrefix = "corso_integration_test/"

// AWSStorageCredEnvs are the variables a gated S3 integration suite
// requires to be non-empty before it runs (see tester.MustGetEnvSets).
// AWS_SESSION_TOKEN is deliberately not among them: only temporary
// credentials carry one. NewPrefixedS3Storage still passes it on when set.
var AWSStorageCredEnvs = []string{
	credentials.AWSAccessKeyID,
	credentials.AWSSecretAccessKey,
}

// NewPrefixedS3Storage returns a storage.Storage for integration tests that
// use S3, with a unique prefix per test.
//
// Where its settings come from - all test configuration, nothing committed:
//   - credentials: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and, if set,
//     AWS_SESSION_TOKEN, read from the environment here. They are the same
//     variables the suites gate on through AWSStorageCredEnvs. S3 storage
//     requires explicit credentials (pkg/storage.S3Config.validate), so a
//     suite run without them fails at its gate, naming the variable.
//   - bucket, endpoint and TLS switch: tconfig.ReadTestConfig - S3_BUCKET or
//     the file named by CORSO_TEST_CONFIG_FILE for the bucket, that file's
//     `endpoint` (default s3.amazonaws.com) and `donotusetls` (default
//     false) for the rest.
//
// Uses t.TempDir() to generate a unique config storage and caching directory for this
// test.  Suites that need to identify this value can retrieve it again from the common
// configs.
func NewPrefixedS3Storage(t tester.TestT) storage.Storage {
	now := tester.LogTimeOfTest(t)

	cfg, err := tconfig.ReadTestConfig()
	require.NoError(t, err, "configuring storage from test file", clues.ToCore(err))

	prefix := testRepoRootPrefix + t.Name() + "-" + now
	t.Logf("testing at s3 bucket [%s] prefix [%s]", cfg[tconfig.TestCfgBucket], prefix)

	st, err := storage.NewStorage(
		storage.ProviderS3,
		&storage.S3Config{
			AWS:         credentials.GetAWS(credentials.GetAWSEnvs()),
			Bucket:      cfg[tconfig.TestCfgBucket],
			Endpoint:    cfg[tconfig.TestCfgEndpoint],
			Prefix:      prefix,
			DoNotUseTLS: str.ParseBool(cfg[tconfig.TestCfgDoNotUseTLS]),
		},
		storage.CommonConfig{
			Corso:       GetAndInsertCorso(""),
			KopiaCfgDir: t.TempDir(),
		})
	require.NoErrorf(t, err, "creating storage: %+v", clues.ToCore(err))

	return st
}

func NewFilesystemStorage(t tester.TestT) storage.Storage {
	now := tester.LogTimeOfTest(t)
	repoPath := filepath.Join(t.TempDir(), now)

	err := os.MkdirAll(repoPath, 0o700)
	require.NoErrorf(t, err, "creating filesystem repo: %+v", clues.ToCore(err))

	t.Logf("testing at filesystem repo [%s]", repoPath)

	st, err := storage.NewStorage(
		storage.ProviderFilesystem,
		&storage.FilesystemConfig{
			Path: repoPath,
		},
		storage.CommonConfig{
			Corso: GetAndInsertCorso(""),
			// Use separate kopia configs for each instance. Place in a new folder to
			// avoid mixing data.
			KopiaCfgDir: t.TempDir(),
		})
	require.NoError(t, err, "creating storage", clues.ToCore(err))

	return st
}

// GetCorso is a helper for aggregating Corso secrets and credentials.
func GetAndInsertCorso(passphase string) credentials.Corso {
	// fetch data from flag, env var or func param giving priority to func param
	// Func param generally will be value fetched from config file using viper.
	corsoPassph := str.First(os.Getenv(credentials.CorsoPassphrase), passphase)

	return credentials.Corso{
		CorsoPassphrase: corsoPassph,
	}
}
