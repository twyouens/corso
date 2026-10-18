package storage

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
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
				// validate() requires credentials unconditionally.
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

// loopbackListener starts a listener on 127.0.0.1 and returns its
// address and a func reporting how many connections it has accepted.
// Nothing in pkg/storage makes a network call today; the listener is a
// boundary guard, so that if this package ever grows one, a
// credential-less config would have to pass through it to be seen here.
// The proof that no *consumer* makes a request is in
// internal/kopia/s3_credentials_test.go and
// pkg/repository/s3_credentials_test.go, which drive the real S3 blob
// store and the real pkg/repository entry points.
func loopbackListener(t *testing.T) (string, func() int) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var (
		mu    sync.Mutex
		conns int
	)

	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			conns++
			mu.Unlock()
			c.Close()
		}
	}()

	t.Cleanup(func() {
		lis.Close()
		<-done
	})

	return lis.Addr().String(), func() int {
		mu.Lock()
		defer mu.Unlock()

		return conns
	}
}

// TestS3Config_ApplyConfigOverrides_RequiresExplicitCredentials replaces
// chunk 0.6's TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials,
// which asserted that the CLI's config path set an opt-out field on the
// struct - and nothing else. That proved nothing about the capability the
// field controlled: round 4 of 0.6's review stopped serialising the
// field, severing the only channel by which it reached its consumers,
// and this test stayed green through the regression. Chunk 0.9 deletes
// the capability instead, on the user's decision, and this test asserts
// it is gone from the CLI's own path: with no credentials anywhere (no
// flag override, no stored config, and the AWS environment variables
// cleared for this test's duration so a developer's real credentials
// cannot make it pass or fail for the wrong reason),
//
//   - ApplyConfigOverrides itself fails, because it ends in validate();
//   - no usable storage.Storage can be constructed from the result; and
//   - nothing reaches the network (see loopbackListener).
//
// The capability that must keep working is asserted too: the same path
// with credentials in the environment succeeds, and yields a Storage.
func (suite *S3CfgUnitSuite) TestS3Config_ApplyConfigOverrides_RequiresExplicitCredentials() {
	endpoint, connections := loopbackListener(suite.T())

	suite.Run("no credentials anywhere", func() {
		t := suite.T()

		for _, key := range []string{
			credentials.AWSAccessKeyID,
			credentials.AWSSecretAccessKey,
			credentials.AWSSessionToken,
		} {
			t.Setenv(key, "")
		}

		c := &S3Config{}
		overrides := map[string]string{Bucket: "bkt", Endpoint: endpoint}

		err := c.ApplyConfigOverrides(testGetter{}, false, false, overrides)
		require.Error(t, err, "the CLI's own config path must require explicit S3 credentials")
		assert.ErrorIs(t, err, errMissingRequired)
		assert.Empty(t, c.AccessKey)
		assert.Empty(t, c.SecretKey)

		_, err = NewStorage(ProviderS3, c)
		require.Error(t, err, "a credential-less S3 config must not produce a usable Storage")

		st := Storage{Provider: ProviderS3, Config: map[string]string{
			keyS3Bucket:   "bkt",
			keyS3Endpoint: endpoint,
		}}

		_, err = st.ToS3Config()
		require.Error(t, err, "nor may a hand-built credential-less config map")

		_, err = st.GetStorageConfigHash()
		require.Error(t, err, "nor may the connect path's config-filename hash")

		assert.Zero(t, connections(), "no request may be made for a credential-less config")
	})

	suite.Run("credentials in the environment", func() {
		t := suite.T()

		t.Setenv(credentials.AWSAccessKeyID, "access")
		t.Setenv(credentials.AWSSecretAccessKey, "secret")
		t.Setenv(credentials.AWSSessionToken, "")

		c := &S3Config{}
		overrides := map[string]string{Bucket: "bkt", Endpoint: endpoint}

		err := c.ApplyConfigOverrides(testGetter{}, false, false, overrides)
		require.NoError(t, err, clues.ToCore(err))

		st, err := NewStorage(ProviderS3, c)
		require.NoError(t, err, clues.ToCore(err))

		out, err := st.ToS3Config()
		require.NoError(t, err, clues.ToCore(err))
		assert.Equal(t, "access", out.AccessKey)
		assert.Equal(t, "secret", out.SecretKey)
	})
}

// TestS3Config_Validate_RequiresCredentials_NoOptOut replaces chunk 0.6's
// TestS3Config_Validate_RejectsBlankCredentialsByDefault, which asserted
// a *default* and therefore also asserted that an opt-out existed. There
// is no opt-out any more: an access key and a secret key are required
// unconditionally, missing, empty and whitespace-only alike.
//
// It also pins the two properties the error itself has to hold, which is
// where a credential check is most tempting to get wrong: the error
// names which field is missing - so a user can act on it - and never
// echoes the value, not even a prefix or a length.
func (suite *S3CfgUnitSuite) TestS3Config_Validate_RequiresCredentials_NoOptOut() {
	const (
		// values that must never appear in an error, in any form
		secretValue = "s3cr3t-value-that-must-never-be-echoed"
		accessValue = "4cce55-value-that-must-never-be-echoed"
		blankTab    = "\t"
	)

	table := []struct {
		name        string
		cfg         S3Config
		expectField string
		neverEchoes []string
	}{
		{
			name:        "missing both",
			cfg:         S3Config{Bucket: "bkt"},
			expectField: accessKeyFieldName,
		},
		{
			// The case that gives the no-echo assertions below
			// something to bite on. Where a *credential* is the missing
			// field its own value is blank by definition, so an error
			// that echoed it would echo nothing; the realistic leak is
			// an error that reports the credentials it did find, or
			// dumps the whole config, while complaining about something
			// else. A missing bucket with both credentials present is
			// exactly that shape.
			name: "missing bucket, credentials present",
			cfg: S3Config{
				AWS: credentials.AWS{AccessKey: accessValue, SecretKey: secretValue},
			},
			expectField: Bucket,
			neverEchoes: []string{accessValue, secretValue},
		},
		{
			name:        "empty access key",
			cfg:         S3Config{Bucket: "bkt", AWS: credentials.AWS{AccessKey: "", SecretKey: secretValue}},
			expectField: accessKeyFieldName,
			neverEchoes: []string{secretValue},
		},
		{
			name: "whitespace-only access key",
			cfg: S3Config{
				Bucket: "bkt",
				AWS:    credentials.AWS{AccessKey: " ", SecretKey: secretValue},
			},
			expectField: accessKeyFieldName,
			neverEchoes: []string{secretValue},
		},
		{
			name: "tab-only access key",
			cfg: S3Config{
				Bucket: "bkt",
				AWS:    credentials.AWS{AccessKey: blankTab, SecretKey: secretValue},
			},
			expectField: accessKeyFieldName,
			neverEchoes: []string{secretValue},
		},
		{
			name:        "missing secret key",
			cfg:         S3Config{Bucket: "bkt", AWS: credentials.AWS{AccessKey: accessValue}},
			expectField: secretKeyFieldName,
			neverEchoes: []string{accessValue},
		},
		{
			name: "whitespace-only secret key",
			cfg: S3Config{
				Bucket: "bkt",
				AWS:    credentials.AWS{AccessKey: accessValue, SecretKey: "   "},
			},
			expectField: secretKeyFieldName,
			neverEchoes: []string{accessValue},
		},
		{
			name: "tab-only secret key",
			cfg: S3Config{
				Bucket: "bkt",
				AWS:    credentials.AWS{AccessKey: accessValue, SecretKey: blankTab},
			},
			expectField: secretKeyFieldName,
			neverEchoes: []string{accessValue},
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			err := test.cfg.validate()
			require.Error(t, err, "no S3 config may validate without both credentials")
			assert.ErrorIs(t, err, errMissingRequired)

			// Every surface a credential could leak through, not just
			// err.Error(): chunk 0.6's review found this error type
			// leaking through some encoders and not others, and a clues
			// error carries values and labels that Error() does not
			// print but a structured logger does. An earlier version of
			// this test checked only Error() and ToCore().Msg, and a
			// mutation that attached both credentials to the error with
			// clues' own .With() passed it unnoticed.
			core := clues.ToCore(err)
			surfaces := map[string]string{
				"Error()":      err.Error(),
				"ToCore().Msg": core.Msg,
				"ToCore()":     fmt.Sprintf("%+v", core),
				"%v":           fmt.Sprintf("%v", err),
				"%+v":          fmt.Sprintf("%+v", err),
				"values":       fmt.Sprintf("%+v", core.Values),
				"labels":       fmt.Sprintf("%+v", core.Labels),
			}

			coreJSON, jsonErr := json.Marshal(core)
			require.NoError(t, jsonErr)
			surfaces["json"] = string(coreJSON)

			msg := err.Error() + " " + core.Msg
			assert.Contains(t, msg, test.expectField, "the error must name the missing field")

			for name, surface := range surfaces {
				for _, value := range test.neverEchoes {
					assert.NotContainsf(t, surface, value,
						"%s must never echo a credential value", name)

					for _, prefixLen := range []int{4, 8} {
						assert.NotContainsf(t, surface, value[:prefixLen],
							"%s must never echo even a prefix of a credential value", name)
					}

					assert.NotContainsf(t, surface, strconv.Itoa(len(value)),
						"%s must never report a credential's length", name)
				}
			}

			// and the same config cannot produce a usable Storage
			cfg := test.cfg
			_, err = NewStorage(ProviderS3, &cfg)
			assert.Error(t, err, "nor may it produce a usable Storage")
		})
	}

	// the one shape that is valid
	ok := S3Config{Bucket: "bkt", AWS: credentials.AWS{AccessKey: "a", SecretKey: "b"}}
	assert.NoError(suite.T(), ok.validate())
}

// TestS3Config_HasNoCredentialOptOutField is a mechanical guard on the
// decision this chunk records: no field on S3Config may offer a way out
// of validate()'s credential requirement. It fails if a field whose name
// suggests one is ever added back, so the next person has to come and
// read this comment - and the changelog entry - before re-adding the
// capability the user deliberately dropped.
func (suite *S3CfgUnitSuite) TestS3Config_HasNoCredentialOptOutField() {
	t := suite.T()
	typ := reflect.TypeOf(S3Config{})

	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, banned := range []string{"ambient", "allow", "insecure", "anonymous", "skipcred"} {
			assert.NotContains(t, name, banned,
				"S3Config must carry no opt-out from its credential requirement")
		}
	}
}

// TestBuildS3ConfigFromMap_NoMapKeyOptsOutOfCredentials keeps chunk 0.6's
// round-4 regression guard, widened. A security review proved that an
// opt-out which round-tripped through the exported Config map let a
// hand-built or rehydrated map carry the flag set with no credentials -
// reaching Kopia's S3 client with whatever identity the process held,
// confirmed live through pkg/repository.Initialize with 13 signed
// requests. The flag is now gone entirely, so no map key can re-enable
// anything; this test asserts that for the eight spellings 0.6's
// security reviewer tried (snake, camel, upper, mixed, unprefixed, and
// with leading and trailing whitespace), so a re-addition under any one
// of them fails here.
//
// These spellings are the only place in src/ that still names the
// removed capability, deliberately: they have to be the literal strings
// an attacker would use for the test to mean anything.
func (suite *S3CfgUnitSuite) TestBuildS3ConfigFromMap_NoMapKeyOptsOutOfCredentials() {
	spellings := []string{
		"s3_allow_ambient_credentials",
		"s3AllowAmbientCredentials",
		"S3_ALLOW_AMBIENT_CREDENTIALS",
		"S3_Allow_Ambient_Credentials",
		"allow_ambient_credentials",
		"AllowAmbientCredentials",
		" s3_allow_ambient_credentials",
		"s3_allow_ambient_credentials ",
	}

	for _, spelling := range spellings {
		suite.Run(spelling, func() {
			t := suite.T()

			forged := map[string]string{
				keyS3Bucket: "bkt",
				spelling:    "true",
			}

			cfg, err := buildS3ConfigFromMap(forged)
			require.Error(t, err, "a credential-less map must fail validation "+
				"whatever opt-out key it carries")
			assert.ErrorIs(t, err, errMissingRequired)
			require.NotNil(t, cfg)
			assert.Empty(t, cfg.AccessKey)
			assert.Empty(t, cfg.SecretKey)
		})
	}

	// and no such key is ever written, so no round trip can produce one
	t := suite.T()

	cfg, err := goodS3Config.StringConfig()
	require.NoError(t, err, clues.ToCore(err))

	for key := range cfg {
		assert.NotContains(t, strings.ToLower(key), "ambient")
		assert.NotContains(t, strings.ToLower(key), "allow")
	}
}
