package storage

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/alcionai/clues"
	"github.com/spf13/cast"

	"github.com/twyouens/corso/src/internal/common"
	"github.com/twyouens/corso/src/internal/common/str"
	"github.com/twyouens/corso/src/pkg/credentials"
)

type S3Config struct {
	credentials.AWS
	Bucket         string // required
	Endpoint       string
	Prefix         string
	DoNotUseTLS    bool
	DoNotVerifyTLS bool
	// AllowAmbientCredentials opts out of this package's own requirement
	// (added to validate(), below) that AccessKey and SecretKey be
	// explicitly set: when false (the default - note this is NOT the
	// default conventional for an "allow" flag, deliberately, so a caller
	// that never touches this field fails closed), an S3Config with
	// either empty is rejected outright rather than being handed to
	// Kopia's S3 blob storage, which falls back silently to its AWS SDK's
	// own ambient-credential chain (environment variables, then IMDS) for
	// any credential left blank - reaching a caller-chosen bucket,
	// prefix and endpoint using the *platform's* identity rather than
	// one the caller actually supplied. The CLI's own ApplyConfigOverrides
	// below sets this true unconditionally: it has always legitimately
	// supported running under a human's or CI job's own AWS identity with
	// no explicit keys, and that must keep working. No other caller in
	// this fork sets it; pkg/engine, specifically, never does.
	AllowAmbientCredentials bool
}

var excludedS3ConfigFieldsForHashing = []string{
	"DoNotUseTLS",
	"DoNotVerifyTLS",
	"AccessKey",
	"SecretKey",
	"SessionToken",
	"AllowAmbientCredentials",
}

// config key consts
const (
	keyS3AccessKey               = "s3_access_key"
	keyS3Bucket                  = "s3_bucket"
	keyS3Endpoint                = "s3_endpoint"
	keyS3Prefix                  = "s3_prefix"
	keyS3SecretKey               = "s3_secret_key"
	keyS3SessionToken            = "s3_session_token"
	keyS3DoNotUseTLS             = "s3_donotusetls"
	keyS3DoNotVerifyTLS          = "s3_donotverifytls"
	keyS3AllowAmbientCredentials = "s3_allow_ambient_credentials"
)

// config exported name consts
const (
	Bucket         = "bucket"
	Endpoint       = "endpoint"
	Prefix         = "prefix"
	DoNotUseTLS    = "donotusetls"
	DoNotVerifyTLS = "donotverifytls"
)

// config file keys
const (
	BucketNameKey             = "bucket"
	EndpointKey               = "endpoint"
	PrefixKey                 = "prefix"
	DisableTLSKey             = "disable_tls"
	DisableTLSVerificationKey = "disable_tls_verification"

	AccessKey       = "aws_access_key_id"
	SecretAccessKey = "aws_secret_access_key"
	SessionToken    = "aws_session_token"
)

var s3constToTomlKeyMap = map[string]string{
	Bucket:                 BucketNameKey,
	Endpoint:               EndpointKey,
	Prefix:                 PrefixKey,
	StorageProviderTypeKey: StorageProviderTypeKey,
}

// add s3 config key names that require path related validations
var s3PathKeys = []string{}

func (s Storage) ToS3Config() (*S3Config, error) {
	return buildS3ConfigFromMap(s.Config)
}

func buildS3ConfigFromMap(config map[string]string) (*S3Config, error) {
	c := &S3Config{}

	if len(config) > 0 {
		c.AccessKey = orEmptyString(config[keyS3AccessKey])
		c.SecretKey = orEmptyString(config[keyS3SecretKey])
		c.SessionToken = orEmptyString(config[keyS3SessionToken])

		c.Bucket = orEmptyString(config[keyS3Bucket])
		c.Endpoint = orEmptyString(config[keyS3Endpoint])
		c.Prefix = orEmptyString(config[keyS3Prefix])
		c.DoNotUseTLS = str.ParseBool(config[keyS3DoNotUseTLS])
		c.DoNotVerifyTLS = str.ParseBool(config[keyS3DoNotVerifyTLS])
		c.AllowAmbientCredentials = str.ParseBool(config[keyS3AllowAmbientCredentials])
	}

	return c, c.validate()
}

func (c *S3Config) normalize() S3Config {
	return S3Config{
		AWS:                     c.AWS,
		Bucket:                  common.NormalizeBucket(c.Bucket),
		Endpoint:                c.Endpoint,
		Prefix:                  common.NormalizePrefix(c.Prefix),
		DoNotUseTLS:             c.DoNotUseTLS,
		DoNotVerifyTLS:          c.DoNotVerifyTLS,
		AllowAmbientCredentials: c.AllowAmbientCredentials,
	}
}

// StringConfig transforms a s3Config struct into a plain
// map[string]string.  All values in the original struct which
// serialize into the map are expected to be strings.
func (c *S3Config) StringConfig() (map[string]string, error) {
	cn := c.normalize()
	cfg := map[string]string{
		keyS3AccessKey:               c.AccessKey,
		keyS3Bucket:                  cn.Bucket,
		keyS3Endpoint:                cn.Endpoint,
		keyS3Prefix:                  cn.Prefix,
		keyS3SecretKey:               c.SecretKey,
		keyS3SessionToken:            c.SessionToken,
		keyS3DoNotUseTLS:             strconv.FormatBool(cn.DoNotUseTLS),
		keyS3DoNotVerifyTLS:          strconv.FormatBool(cn.DoNotVerifyTLS),
		keyS3AllowAmbientCredentials: strconv.FormatBool(cn.AllowAmbientCredentials),
	}

	return cfg, cn.validate()
}

// validate requires Bucket always, and - unless AllowAmbientCredentials is
// set - a non-blank AccessKey and SecretKey too. See
// AllowAmbientCredentials' own doc comment for why: without this, an
// S3Config with no credentials reaches Kopia's S3 blob storage, which
// falls back silently to its AWS SDK's own ambient-credential chain,
// signing requests with the platform's identity rather than one the
// caller actually supplied - confirmed live. Whitespace-only values
// (" ", "	") are treated as blank: they are not valid AWS credentials
// either, and would otherwise pass this check only to fail later, after
// several signed-but-rejected round trips against the caller-named
// endpoint, with a confusing "bucket does not exist" rather than a clear
// error here.
func (c S3Config) validate() error {
	check := map[string]string{
		Bucket: c.Bucket,
	}
	for k, v := range check {
		if len(v) == 0 {
			return clues.Stack(errMissingRequired, clues.New(k))
		}
	}

	if !c.AllowAmbientCredentials {
		if len(strings.TrimSpace(c.AccessKey)) == 0 || len(strings.TrimSpace(c.SecretKey)) == 0 {
			return clues.Stack(
				errMissingRequired,
				clues.New("accessKey/secretKey (or set AllowAmbientCredentials to use ambient AWS credentials)"))
		}
	}

	return nil
}

func (c S3Config) configHash() (string, error) {
	filteredS3Config := createFilteredS3ConfigForHashing(c)

	b, err := json.Marshal(filteredS3Config)
	if err != nil {
		return "", clues.Stack(err)
	}

	return str.GenerateHash(b), nil
}

func createFilteredS3ConfigForHashing(source S3Config) map[string]any {
	filteredS3Config := make(map[string]any)
	sourceValue := reflect.ValueOf(source)

	for i := 0; i < sourceValue.NumField(); i++ {
		fieldName := sourceValue.Type().Field(i).Name
		if !slices.Contains(excludedS3ConfigFieldsForHashing, fieldName) {
			filteredS3Config[fieldName] = sourceValue.Field(i).Interface()
		}
	}

	return filteredS3Config
}

func s3Overrides(in map[string]string) map[string]string {
	return map[string]string{
		Bucket:                 in[Bucket],
		Endpoint:               in[Endpoint],
		Prefix:                 in[Prefix],
		DoNotUseTLS:            in[DoNotUseTLS],
		DoNotVerifyTLS:         in[DoNotVerifyTLS],
		StorageProviderTypeKey: in[StorageProviderTypeKey],
	}
}

func (c *S3Config) s3ConfigsFromStore(kvg Getter) {
	c.Bucket = cast.ToString(kvg.Get(BucketNameKey))
	c.Endpoint = cast.ToString(kvg.Get(EndpointKey))
	c.Prefix = cast.ToString(kvg.Get(PrefixKey))
	c.DoNotUseTLS = cast.ToBool(kvg.Get(DisableTLSKey))
	c.DoNotVerifyTLS = cast.ToBool(kvg.Get(DisableTLSVerificationKey))
}

func (c *S3Config) s3CredsFromStore(kvg Getter) {
	c.AccessKey = cast.ToString(kvg.Get(AccessKey))
	c.SecretKey = cast.ToString(kvg.Get(SecretAccessKey))
	c.SessionToken = cast.ToString(kvg.Get(SessionToken))
}

var _ Configurer = &S3Config{}

func (c *S3Config) ApplyConfigOverrides(
	kvg Getter,
	readConfigFromStore bool,
	matchFromConfig bool,
	overrides map[string]string,
) error {
	if readConfigFromStore {
		c.s3ConfigsFromStore(kvg)

		if b, ok := overrides[Bucket]; ok {
			overrides[Bucket] = common.NormalizeBucket(b)
		}

		if p, ok := overrides[Prefix]; ok {
			overrides[Prefix] = common.NormalizePrefix(p)
		}

		if matchFromConfig {
			providerType := cast.ToString(kvg.Get(StorageProviderTypeKey))
			if providerType != ProviderS3.String() {
				return clues.New("unsupported storage provider: [" + providerType + "]")
			}

			if err := mustMatchConfig(kvg, s3constToTomlKeyMap, s3Overrides(overrides), s3PathKeys); err != nil {
				return clues.Stack(err)
			}
		}
	}

	c.s3CredsFromStore(kvg)

	aws := credentials.AWS{
		AccessKey: str.First(
			overrides[credentials.AWSAccessKeyID],
			os.Getenv(credentials.AWSAccessKeyID),
			c.AccessKey),
		SecretKey: str.First(
			overrides[credentials.AWSSecretAccessKey],
			os.Getenv(credentials.AWSSecretAccessKey),
			c.SecretKey),
		SessionToken: str.First(
			overrides[credentials.AWSSessionToken],
			os.Getenv(credentials.AWSSessionToken),
			c.SessionToken),
	}

	c.AWS = aws
	c.Bucket = str.First(overrides[Bucket], c.Bucket)
	c.Endpoint = str.First(overrides[Endpoint], c.Endpoint, "s3.amazonaws.com")
	c.Prefix = str.First(overrides[Prefix], c.Prefix)
	c.DoNotUseTLS = str.ParseBool(str.First(
		overrides[DoNotUseTLS],
		strconv.FormatBool(c.DoNotUseTLS),
		"false"))
	c.DoNotVerifyTLS = str.ParseBool(str.First(
		overrides[DoNotVerifyTLS],
		strconv.FormatBool(c.DoNotVerifyTLS),
		"false"))

	// The CLI's own config path, and only the CLI's: ApplyConfigOverrides
	// is reached exclusively through pkg/config.ReadCorsoConfig/
	// configureStorage, called only from cli/repo's command handlers and
	// the cmd/longevity_test and cmd/s3checker developer tools - never
	// from pkg/engine, which builds a Storage directly via
	// storage.NewStorage and never touches this method. The CLI has
	// always legitimately supported running with no explicit S3
	// credentials (environment variables, an EC2/ECS instance role);
	// this keeps that working now that validate() requires
	// AccessKey/SecretKey by default. Deliberately unconditional, not
	// tied to a flag or config-file value: this is restoring the CLI's
	// own pre-existing behavior, not a new user-facing choice.
	c.AllowAmbientCredentials = true

	return c.validate()
}

var _ WriteConfigToStorer = &S3Config{}

func (c *S3Config) WriteConfigToStore(
	kvs Setter,
) {
	s3Config := c.normalize()

	kvs.Set(StorageProviderTypeKey, ProviderS3.String())
	kvs.Set(BucketNameKey, s3Config.Bucket)
	kvs.Set(EndpointKey, s3Config.Endpoint)
	kvs.Set(PrefixKey, s3Config.Prefix)
	kvs.Set(DisableTLSKey, s3Config.DoNotUseTLS)
	kvs.Set(DisableTLSVerificationKey, s3Config.DoNotVerifyTLS)
}
