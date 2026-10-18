package storage

import (
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/credentials"
)

type testConfig struct {
	expect string
	err    error
}

func (c testConfig) StringConfig() (map[string]string, error) {
	return map[string]string{"expect": c.expect}, c.err
}

type StorageUnitSuite struct {
	tester.Suite
}

func TestStorageUnitSuite(t *testing.T) {
	suite.Run(t, &StorageUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *StorageUnitSuite) TestNewStorage() {
	table := []struct {
		name     string
		p        ProviderType
		c        testConfig
		errCheck assert.ErrorAssertionFunc
	}{
		{"unknown no error", ProviderUnknown, testConfig{"configVal", nil}, assert.NoError},
		{"s3 no error", ProviderS3, testConfig{"configVal", nil}, assert.NoError},
		{"unknown w/ error", ProviderUnknown, testConfig{"configVal", assert.AnError}, assert.Error},
		{"s3 w/ error", ProviderS3, testConfig{"configVal", assert.AnError}, assert.Error},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			s, err := NewStorage(test.p, test.c)
			test.errCheck(t, err, clues.ToCore(err))

			// remaining tests are dependent upon error-free state
			if test.c.err != nil {
				return
			}

			assert.Equalf(t,
				test.p,
				s.Provider,
				"expected storage provider [%s], got [%s]", test.p, s.Provider)
			assert.Equalf(t,
				test.c.expect,
				s.Config["expect"],
				"expected storage config [%s], got [%s]", test.c.expect, s.Config["expect"])
		})
	}
}

func (suite *StorageUnitSuite) TestGetAccountConfigHash() {
	tests := []struct {
		name     string
		provider ProviderType
		config   any
	}{
		{
			name:     "s3 storage",
			provider: ProviderS3,
			config:   getTestS3Config("test-bucket", "https://aws.s3", "test-prefix"),
		},
		{
			name:     "filesystem storage",
			provider: ProviderFilesystem,
			config:   getTestFileSystemConfig("test/to/dir"),
		},
		{
			name:     "invalid account",
			provider: ProviderUnknown,
			config:   testConfig{"configVal", nil},
		},
	}

	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			if test.provider == ProviderUnknown {
				s, err := NewStorage(test.provider, test.config.(testConfig))
				require.NoError(t, err)

				_, err = s.GetStorageConfigHash()
				require.Error(t, err)
			}

			if test.provider == ProviderS3 {
				_, ok := test.config.(Configurer)
				require.True(t, ok)

				s3Cnf := test.config.(*S3Config)
				s, err := NewStorage(test.provider, s3Cnf)
				require.NoError(t, err)

				hash, err := s.GetStorageConfigHash()
				require.NoError(t, err)
				assert.True(t, len(hash) > 0)
			}

			if test.provider == ProviderFilesystem {
				_, ok := test.config.(Configurer)
				require.True(t, ok)

				fsCnf := test.config.(*FilesystemConfig)
				s, err := NewStorage(test.provider, fsCnf)
				require.NoError(t, err)

				hash, err := s.GetStorageConfigHash()
				require.NoError(t, err)
				assert.True(t, len(hash) > 0)
			}
		})
	}
}

func getTestS3Config(bucket, endpoint, prefix string) *S3Config {
	return &S3Config{
		Bucket:   bucket,
		Endpoint: endpoint,
		Prefix:   prefix,
		// Non-blank: validate() requires an access key and a secret key
		// unconditionally (see s3.go's validate), so this fixture needs
		// them even though its callers are hash tests. Note that these
		// values DO affect the hash, despite
		// excludedS3ConfigFieldsForHashing naming "AccessKey" and
		// "SecretKey": exclusion matches top-level field names, and the
		// embedded credentials.AWS is a single field named "AWS", which
		// is hashed whole. See
		// TestGetStorageConfigHash_PinnedValues, which pins both facts.
		AWS: credentials.AWS{AccessKey: "test-access-key", SecretKey: "test-secret-key"},
	}
}

func getTestFileSystemConfig(path string) *FilesystemConfig {
	return &FilesystemConfig{
		Path: path,
	}
}

type testGetter struct {
	storeMap map[string]string
}

func (tg testGetter) Get(key string) any {
	val, ok := tg.storeMap[key]
	if ok {
		return val
	}

	return ""
}

func (suite *StorageUnitSuite) TestMustMatchConfig() {
	t := suite.T()

	table := []struct {
		name        string
		tomlMap     map[string]string
		overrideMap map[string]string
		getterMap   map[string]string
		pathKeys    []string
		errorCheck  assert.ErrorAssertionFunc
	}{
		{
			name:    "s3 config match",
			tomlMap: s3constToTomlKeyMap,
			overrideMap: map[string]string{
				Bucket:           "test-bucket",
				Endpoint:         "https://aws.s3",
				Prefix:           "test-prefix",
				"additional-key": "additional-value",
			},
			getterMap: map[string]string{
				"bucket":   "test-bucket",
				"endpoint": "https://aws.s3",
				"prefix":   "test-prefix",
			},
			errorCheck: assert.NoError,
		},
		{
			name:    "s3 config match - bucket mismatch",
			tomlMap: s3constToTomlKeyMap,
			overrideMap: map[string]string{
				Bucket:           "test-bucket",
				Endpoint:         "https://aws.s3",
				Prefix:           "test-prefix",
				"additional-key": "additional-value",
			},
			getterMap: map[string]string{
				"bucket":   "test-bucket-new",
				"endpoint": "https://aws.s3",
				"prefix":   "test-prefix",
			},
			errorCheck: assert.Error,
		},
		{
			name:    "s3 config match - endpoint mismatch",
			tomlMap: s3constToTomlKeyMap,
			overrideMap: map[string]string{
				Bucket:           "test-bucket",
				Endpoint:         "https://aws.s3",
				Prefix:           "test-prefix",
				"additional-key": "additional-value",
			},
			getterMap: map[string]string{
				"bucket":   "test-bucket",
				"endpoint": "https://aws.s3/new",
				"prefix":   "test-prefix",
			},
			errorCheck: assert.Error,
		},
		{
			name:    "s3 config match - prefix mismatch",
			tomlMap: s3constToTomlKeyMap,
			overrideMap: map[string]string{
				Bucket:           "test-bucket",
				Endpoint:         "https://aws.s3",
				Prefix:           "test-prefix",
				"additional-key": "additional-value",
			},
			getterMap: map[string]string{
				"bucket":   "test-bucket",
				"endpoint": "https://aws.s3",
				"prefix":   "test-prefix-new",
			},
			errorCheck: assert.Error,
		},
		{
			name:    "filesystem config match - success case",
			tomlMap: fsConstToTomlKeyMap,
			overrideMap: map[string]string{
				StorageProviderTypeKey: "filesystem",
				FilesystemPath:         "/path/to/dir",
				"additional-key":       "additional-value",
			},
			getterMap: map[string]string{
				"provider": "filesystem",
				"path":     "/path/to/dir",
			},
			pathKeys:   []string{FilesystemPath},
			errorCheck: assert.NoError,
		},
		{
			name:    "filesystem config match - path mismatch",
			tomlMap: fsConstToTomlKeyMap,
			overrideMap: map[string]string{
				StorageProviderTypeKey: "filesystem",
				FilesystemPath:         "/path/to/dir",
				"additional-key":       "additional-value",
			},
			getterMap: map[string]string{
				"provider": "filesystem",
				"path":     "/path/to/dir/new",
			},
			pathKeys:   []string{FilesystemPath},
			errorCheck: assert.Error,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			tg := testGetter{test.getterMap}
			err := mustMatchConfig(tg, test.tomlMap, test.overrideMap, test.pathKeys)
			test.errorCheck(t, err)
		})
	}
}

// TestGetStorageConfigHash_PinnedValues pins the config hashes for two
// fixed configs by exact value. The repository's own config filename is
// derived from these (pkg/repository's
// GenerateHashForRepositoryConfigFileName), so a shift strands every
// existing repository: a change here must be a deliberate, recorded
// decision, not a side effect.
//
// Measured at this chunk's base commit (cf74058f) and at its head, for
// the same fixtures, and identical at both. Chunk 0.9 removes a struct
// field from S3Config, which cannot move the hash because the field was
// named in excludedS3ConfigFieldsForHashing - but "cannot" is the kind
// of claim this session has learned to measure rather than assert.
//
// The second half pins something a reader would otherwise get wrong:
// the hash is credential-sensitive. createFilteredS3ConfigForHashing
// filters by top-level field name, and credentials.AWS is embedded, so
// it appears as one field named "AWS" and is hashed whole;
// excludedS3ConfigFieldsForHashing's "AccessKey", "SecretKey" and
// "SessionToken" entries never match anything. That is pre-existing
// behaviour, untouched here, and it means rotating an access key moves a
// repository's config filename. Pinning it means anyone who "fixes" the
// exclusion list has to notice that consequence first.
func (suite *StorageUnitSuite) TestGetStorageConfigHash_PinnedValues() {
	t := suite.T()

	s3, err := NewStorage(ProviderS3, getTestS3Config("test-bucket", "https://aws.s3", "test-prefix"))
	require.NoError(t, err, clues.ToCore(err))

	s3Hash, err := s3.GetStorageConfigHash()
	require.NoError(t, err, clues.ToCore(err))
	assert.Equal(t, "e1e21c55", s3Hash, "the s3 config hash must not move")

	fs, err := NewStorage(ProviderFilesystem, getTestFileSystemConfig("test/to/dir"))
	require.NoError(t, err, clues.ToCore(err))

	fsHash, err := fs.GetStorageConfigHash()
	require.NoError(t, err, clues.ToCore(err))
	assert.Equal(t, "c1074a13", fsHash, "the filesystem config hash must not move")

	// a credential-less config can no longer reach GetStorageConfigHash
	// (ToS3Config validates first), so hash it directly: this is the
	// config shape whose hash the field removal could most plausibly
	// have moved.
	bare := S3Config{Bucket: "test-bucket", Endpoint: "https://aws.s3", Prefix: "test-prefix/"}

	bareHash, err := bare.configHash()
	require.NoError(t, err, clues.ToCore(err))
	assert.Equal(t, "0a1061de", bareHash, "the credential-less s3 config hash must not move")

	// credential-sensitivity, pinned deliberately: see the doc comment.
	rotated := *getTestS3Config("test-bucket", "https://aws.s3", "test-prefix")
	rotated.AccessKey = "rotated-access-key"

	rotatedHash, err := rotated.configHash()
	require.NoError(t, err, clues.ToCore(err))
	assert.NotEqual(t, bareHash, rotatedHash)
	assert.NotEqual(t, "e1e21c55", rotatedHash,
		"the hash is credential-sensitive today; excludedS3ConfigFieldsForHashing's "+
			"AccessKey/SecretKey/SessionToken entries do not match the embedded AWS field")
}
