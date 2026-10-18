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
	// AccessKey and SecretKey, embedded here from credentials.AWS, are
	// both required, with no opt-out anywhere in this fork - see
	// validate().
	credentials.AWS
	Bucket         string // required
	Endpoint       string
	Prefix         string
	DoNotUseTLS    bool
	DoNotVerifyTLS bool
}

var excludedS3ConfigFieldsForHashing = []string{
	"DoNotUseTLS",
	"DoNotVerifyTLS",
	"AccessKey",
	"SecretKey",
	"SessionToken",
}

// config key consts
const (
	keyS3AccessKey      = "s3_access_key"
	keyS3Bucket         = "s3_bucket"
	keyS3Endpoint       = "s3_endpoint"
	keyS3Prefix         = "s3_prefix"
	keyS3SecretKey      = "s3_secret_key"
	keyS3SessionToken   = "s3_session_token"
	keyS3DoNotUseTLS    = "s3_donotusetls"
	keyS3DoNotVerifyTLS = "s3_donotverifytls"
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

// field names validate() names in its errors. They identify which
// configuration value is missing; they never carry its value.
const (
	accessKeyFieldName = "accessKey"
	secretKeyFieldName = "secretKey"
)

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
	}

	return c, c.validate()
}

func (c *S3Config) normalize() S3Config {
	return S3Config{
		AWS:            c.AWS,
		Bucket:         common.NormalizeBucket(c.Bucket),
		Endpoint:       c.Endpoint,
		Prefix:         common.NormalizePrefix(c.Prefix),
		DoNotUseTLS:    c.DoNotUseTLS,
		DoNotVerifyTLS: c.DoNotVerifyTLS,
	}
}

// StringConfig transforms a s3Config struct into a plain
// map[string]string.  All values in the original struct which
// serialize into the map are expected to be strings.
func (c *S3Config) StringConfig() (map[string]string, error) {
	cn := c.normalize()
	cfg := map[string]string{
		keyS3AccessKey:      c.AccessKey,
		keyS3Bucket:         cn.Bucket,
		keyS3Endpoint:       cn.Endpoint,
		keyS3Prefix:         cn.Prefix,
		keyS3SecretKey:      c.SecretKey,
		keyS3SessionToken:   c.SessionToken,
		keyS3DoNotUseTLS:    strconv.FormatBool(cn.DoNotUseTLS),
		keyS3DoNotVerifyTLS: strconv.FormatBool(cn.DoNotVerifyTLS),
	}

	return cfg, cn.validate()
}

// validate requires Bucket, AccessKey and SecretKey. There is no opt-out,
// anywhere in this fork, for any caller, including the CLI.
//
// Without the credential requirement, an S3Config with a blank access or
// secret key reaches Kopia's S3 blob storage, which falls back silently
// to its AWS SDK's own credential chain - environment variables, then a
// shared credentials file or profile, then the EC2/ECS instance role via
// IMDS - for any credential left blank. A caller-chosen bucket, prefix
// and endpoint is then served by whatever identity the *process* happens
// to carry rather than one the caller actually supplied. Chunk 0.6's
// security review proved that live against a real endpoint, and chunk
// 0.9 removed the capability outright rather than keeping an opt-out:
// credentials for S3 are always explicit here. Supplying them through
// the AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY environment variables
// still works for the CLI, because ApplyConfigOverrides reads those two
// variables itself and the resulting values are checked here like any
// other; what is gone is the silent fallback *inside* Kopia's SDK to any
// credential source this code never saw.
//
// Whitespace-only values (" ", "\t") are treated as blank: they are not
// valid AWS credentials either, and would otherwise pass this check only
// to fail later, after several signed-but-rejected round trips against
// the caller-named endpoint, with a confusing "bucket does not exist"
// rather than a clear error here.
//
// Each error names the configuration field that is missing and never
// echoes its value, not even a prefix or a length.
func (c S3Config) validate() error {
	if len(c.Bucket) == 0 {
		return clues.Stack(errMissingRequired, clues.New(Bucket))
	}

	if len(strings.TrimSpace(c.AccessKey)) == 0 {
		return clues.Stack(errMissingRequired, clues.New(accessKeyFieldName))
	}

	if len(strings.TrimSpace(c.SecretKey)) == 0 {
		return clues.Stack(errMissingRequired, clues.New(secretKeyFieldName))
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

	// Note what is deliberately absent here: there is no opt-out from
	// validate()'s credential requirement, for the CLI or anyone else.
	// Chunk 0.6 added one - a bool field on this struct, set
	// unconditionally on this path - so a CLI user on an EC2/ECS instance
	// role could keep connecting with no explicit keys; chunk 0.9 deleted
	// the field and the capability, on the user's decision, rather than
	// carry an opt-out whose only purpose was to re-enable the credential
	// fallback a security review had just proven dangerous. Explicit
	// credentials - from --aws-access-key, the config file, or the
	// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY environment variables read
	// just above - are now required for S3.

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
