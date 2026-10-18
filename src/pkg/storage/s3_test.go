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

type S3CfgUnitSuite struct {
	tester.Suite
}

func TestS3CfgUnitSuite(t *testing.T) {
	suite.Run(t, &S3CfgUnitSuite{Suite: tester.NewUnitSuite(t)})
}

var (
	goodS3Config = S3Config{
		Bucket:         "bkt",
		Endpoint:       "end",
		Prefix:         "pre/",
		DoNotUseTLS:    false,
		DoNotVerifyTLS: false,
		AWS:            credentials.AWS{AccessKey: "access", SecretKey: "secret", SessionToken: "token"},
	}

	goodS3Map = map[string]string{
		keyS3Bucket:         "bkt",
		keyS3Endpoint:       "end",
		keyS3Prefix:         "pre/",
		keyS3DoNotUseTLS:    "false",
		keyS3DoNotVerifyTLS: "false",
		keyS3AccessKey:      "access",
		keyS3SecretKey:      "secret",
		keyS3SessionToken:   "token",
	}
)

func (suite *S3CfgUnitSuite) TestS3Config_Config() {
	s3 := goodS3Config

	c, err := s3.StringConfig()
	assert.NoError(suite.T(), err, clues.ToCore(err))

	table := []struct {
		key    string
		expect string
	}{
		{"s3_bucket", s3.Bucket},
		{"s3_endpoint", s3.Endpoint},
		{"s3_prefix", s3.Prefix},
	}
	for _, test := range table {
		assert.Equal(suite.T(), test.expect, c[test.key])
	}
}

func (suite *S3CfgUnitSuite) TestStorage_S3Config() {
	t := suite.T()
	in := goodS3Config

	s, err := NewStorage(ProviderS3, &in)
	assert.NoError(t, err, clues.ToCore(err))

	out, err := s.ToS3Config()
	assert.NoError(t, err, clues.ToCore(err))

	assert.Equal(t, in.Bucket, out.Bucket)
	assert.Equal(t, in.Endpoint, out.Endpoint)
	assert.Equal(t, in.Prefix, out.Prefix)
}

func makeTestS3Cfg(bkt, end, pre, access, secret, session string) S3Config {
	return S3Config{
		Bucket:   bkt,
		Endpoint: end,
		Prefix:   pre,
		AWS:      credentials.AWS{AccessKey: access, SecretKey: secret, SessionToken: session},
	}
}

func (suite *S3CfgUnitSuite) TestStorage_S3Config_invalidCases() {
	// missing required properties
	table := []struct {
		name string
		cfg  S3Config
	}{
		{"missing bucket", makeTestS3Cfg("", "end", "pre/", "", "", "")},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			_, err := NewStorage(ProviderUnknown, &test.cfg)
			assert.Error(suite.T(), err)
		})
	}

	// required property not populated in storage
	table2 := []struct {
		name  string
		amend func(Storage)
	}{
		{
			"missing bucket",
			func(s Storage) {
				s.Config["s3_bucket"] = ""
			},
		},
	}
	for _, test := range table2 {
		suite.Run(test.name, func() {
			t := suite.T()

			st, err := NewStorage(ProviderUnknown, &goodS3Config)
			assert.NoError(t, err, clues.ToCore(err))
			test.amend(st)

			_, err = st.ToS3Config()
			assert.Error(t, err, clues.ToCore(err))
		})
	}
}

func (suite *S3CfgUnitSuite) TestStorage_S3Config_StringConfig() {
	table := []struct {
		name   string
		input  S3Config
		expect map[string]string
	}{
		{
			name:   "standard",
			input:  goodS3Config,
			expect: goodS3Map,
		},
		{
			name: "normalized bucket name",
			input: makeTestS3Cfg(
				"s3://"+goodS3Config.Bucket,
				goodS3Config.Endpoint,
				goodS3Config.Prefix,
				goodS3Config.AccessKey,
				goodS3Config.SecretKey,
				goodS3Config.SessionToken),
			expect: goodS3Map,
		},
		{
			name: "disabletls",
			input: S3Config{
				Bucket:         "bkt",
				Endpoint:       "end",
				Prefix:         "pre/",
				DoNotUseTLS:    true,
				DoNotVerifyTLS: true,
				// Non-blank, so this case still exercises what it is
				// actually about (TLS flag serialization), now that
				// validate() requires credentials by default - see
				// AllowAmbientCredentials in s3.go.
				AWS: credentials.AWS{AccessKey: "access", SecretKey: "secret"},
			},
			expect: map[string]string{
				keyS3Bucket:         "bkt",
				keyS3Endpoint:       "end",
				keyS3Prefix:         "pre/",
				keyS3DoNotUseTLS:    "true",
				keyS3DoNotVerifyTLS: "true",
				keyS3AccessKey:      "access",
				keyS3SecretKey:      "secret",
				keyS3SessionToken:   "",
			},
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			result, err := test.input.StringConfig()
			require.NoError(t, err, clues.ToCore(err))
			assert.Equal(t, test.expect, result)
		})
	}
}

func (suite *S3CfgUnitSuite) TestStorage_S3Config_Normalize() {
	const (
		prefixedBkt = "s3://bkt"
		normalBkt   = "bkt"
	)

	st := S3Config{
		Bucket: prefixedBkt,
	}

	result := st.normalize()
	assert.Equal(suite.T(), normalBkt, result.Bucket)
	assert.NotEqual(suite.T(), st.Bucket, result.Bucket)
}

// TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials proves
// chunk 0.6's review round-3 fix: ApplyConfigOverrides - reached only
// through the CLI's own pkg/config.ReadCorsoConfig/configureStorage, and
// the cmd/longevity_test and cmd/s3checker developer tools, never through
// pkg/engine - keeps working with no explicit AWS credentials anywhere
// (no override, no stored config, and this test clears the two AWS env
// vars for its own duration so a developer's real credentials in the
// environment can't accidentally make this pass for the wrong reason).
// validate()'s new default requirement for AccessKey/SecretKey must not
// break this path: ApplyConfigOverrides sets AllowAmbientCredentials
// unconditionally, specifically so this keeps succeeding.
func (suite *S3CfgUnitSuite) TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials() {
	t := suite.T()

	for _, key := range []string{credentials.AWSAccessKeyID, credentials.AWSSecretAccessKey, credentials.AWSSessionToken} {
		t.Setenv(key, "")
	}

	c := &S3Config{}

	overrides := map[string]string{
		Bucket: "bkt",
	}

	err := c.ApplyConfigOverrides(testGetter{}, false, false, overrides)
	require.NoError(t, err, clues.ToCore(err))

	assert.True(t, c.AllowAmbientCredentials, "the CLI's own path must set AllowAmbientCredentials")
	assert.Empty(t, c.AccessKey)
	assert.Empty(t, c.SecretKey)
}

// TestS3Config_Validate_RejectsBlankCredentialsByDefault proves the
// shared validator's own new default: with AllowAmbientCredentials unset
// (false, the zero value), a Bucket-only S3Config is rejected for a
// blank AccessKey/SecretKey - the gap the security reviewer proved was
// reachable even through pkg/repository directly, bypassing pkg/engine
// entirely. Setting AllowAmbientCredentials explicitly is what the CLI's
// own ApplyConfigOverrides does (see the sibling test above); nothing
// else in this fork may.
func (suite *S3CfgUnitSuite) TestS3Config_Validate_RejectsBlankCredentialsByDefault() {
	t := suite.T()

	blank := S3Config{Bucket: "bkt"}
	err := blank.validate()
	require.Error(t, err)

	allowed := S3Config{Bucket: "bkt", AllowAmbientCredentials: true}
	assert.NoError(t, allowed.validate())

	withCreds := S3Config{Bucket: "bkt", AWS: credentials.AWS{AccessKey: "a", SecretKey: "b"}}
	assert.NoError(t, withCreds.validate())
}

// TestBuildS3ConfigFromMap_IgnoresAllowAmbientCredentialsKey proves chunk
// 0.6's review round-4 fix: a security review found that
// AllowAmbientCredentials round-tripped through the exported Config map
// (StringConfig wrote it, buildS3ConfigFromMap read it back), so a
// hand-built or rehydrated map carrying the flag set and no credentials
// passed ToS3Config/validate - reaching Kopia's S3 client with the
// platform's ambient AWS identity rather than a caller-supplied one,
// confirmed live through pkg/repository.Initialize even though
// pkg/engine's own validateStorageCredentials rejected the identical
// forged map outright.
//
// AllowAmbientCredentials is now a Go-only field, never serialised to or
// read back from the map. This test builds exactly the forged map a prior
// version of buildS3ConfigFromMap would have honoured - bucket only, no
// credentials, the ambient-credentials key set true - and asserts it
// still fails validation: the key is inert from this map's perspective,
// and the only way to set the real field is to hold a *S3Config directly
// (ApplyConfigOverrides, and nothing else in this fork).
func (suite *S3CfgUnitSuite) TestBuildS3ConfigFromMap_IgnoresAllowAmbientCredentialsKey() {
	t := suite.T()

	forged := map[string]string{
		keyS3Bucket:                    "bkt",
		"s3_allow_ambient_credentials": "true",
	}

	cfg, err := buildS3ConfigFromMap(forged)
	require.Error(t, err, "a credential-less map must still fail validation "+
		"regardless of any ambient-credentials key it carries")
	require.NotNil(t, cfg)
	assert.False(t, cfg.AllowAmbientCredentials,
		"the forged key must never be read back into the struct field")
}
