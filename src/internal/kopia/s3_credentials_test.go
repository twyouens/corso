package kopia

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/pkg/control/repository"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/storage"
	storeTD "github.com/twyouens/corso/src/pkg/storage/testdata"
)

// TestS3BlobStorage_RefusesCredentialLessConfig drives chunk 0.6's
// security review attack into the code that actually consumes an S3
// config - s3BlobStorage, the one function in this fork that hands
// credentials to Kopia's S3 blob store, and therefore the last place a
// blank credential could still fall through to the AWS SDK's own
// credential chain (environment, shared credentials file, EC2/ECS
// instance role).
//
// Fake credentials are in the process environment for the test's
// duration, the endpoint is a loopback recorder the test owns, and the
// config carries no credentials of its own. The requirement: an error,
// no request to the endpoint, and no Authorization header emitted.
//
// This is the companion to pkg/repository's own reproduction. That one
// covers the public, importable route a caller takes; this one covers
// the consumer at the bottom of it, so neither a new caller nor a change
// of route can quietly re-open the hole.
func TestS3BlobStorage_RefusesCredentialLessConfig(t *testing.T) {
	t.Setenv(credentials.AWSAccessKeyID, "AKIAAMBIENTTESTKEY00")
	t.Setenv(credentials.AWSSecretAccessKey, "ambient-test-secret-key-not-a-real-credential")
	t.Setenv(credentials.AWSSessionToken, "")

	rec := storeTD.NewS3RequestRecorder(t)
	ctx := context.Background()

	table := []struct {
		name      string
		accessKey string
		secretKey string
		// forgedKey, when set, is a spelling of the opt-out key chunk
		// 0.6's security reviewer forged into the config map. No
		// spelling may mean anything now that the capability is gone.
		forgedKey string
	}{
		{name: "both missing"},
		{name: "access key missing", secretKey: "secret"},
		{name: "secret key missing", accessKey: "access"},
		{name: "access key whitespace only", accessKey: " ", secretKey: "secret"},
		{name: "secret key whitespace only", accessKey: "access", secretKey: "\t"},
		{name: "forged opt-out key, snake", forgedKey: "s3_allow_ambient_credentials"},
		{name: "forged opt-out key, camel", forgedKey: "s3AllowAmbientCredentials"},
		{name: "forged opt-out key, upper", forgedKey: "S3_ALLOW_AMBIENT_CREDENTIALS"},
		{name: "forged opt-out key, mixed", forgedKey: "S3_Allow_Ambient_Credentials"},
		{name: "forged opt-out key, unprefixed", forgedKey: "allow_ambient_credentials"},
		{name: "forged opt-out key, go field name", forgedKey: "AllowAmbientCredentials"},
		{name: "forged opt-out key, leading space", forgedKey: " s3_allow_ambient_credentials"},
		{name: "forged opt-out key, trailing space", forgedKey: "s3_allow_ambient_credentials "},
	}
	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			st := storage.Storage{
				Provider: storage.ProviderS3,
				Config: map[string]string{
					"s3_bucket":      "credential-less-bucket",
					"s3_endpoint":    rec.Endpoint,
					"s3_donotusetls": "true",
					"s3_access_key":  test.accessKey,
					"s3_secret_key":  test.secretKey,
				},
			}

			if len(test.forgedKey) > 0 {
				st.Config[test.forgedKey] = "true"
			}

			// Deliberately assert, not require: the recorder assertions
			// after this loop are what observe a request escaping to the
			// endpoint, and a FailNow here would stop them being
			// reached in exactly the case that matters.
			store, err := s3BlobStorage(ctx, repository.Options{}, st)
			if assert.Error(t, err, "S3 blob storage must refuse a config without explicit credentials") {
				assert.Contains(t, err.Error(), "missing required storage configuration")
			}

			assert.Nil(t, store)
		})
	}

	assert.Empty(t, rec.Requests(),
		"no credential-less config may reach a caller-named S3 endpoint")
	assert.False(t, rec.SawAuthorization(),
		"nothing may sign a request with a credential this code was never given")

	// The positive control, on its own recorder, and the reason the
	// assertions above mean anything: given *explicit* credentials, this
	// exact code path does reach the endpoint and does sign the request.
	// Kopia's s3.New verifies the bucket when it constructs the store, so
	// a config that gets past the credential check is observable here
	// within milliseconds. Measured: one signed "GET
	// /credential-less-bucket/". So "zero requests" above is a real
	// measurement of a path that would otherwise be seen, not an artefact
	// of an instrument that cannot see anything.
	t.Run("positive control: explicit credentials do reach the endpoint", func(t *testing.T) {
		ctrl := storeTD.NewS3RequestRecorder(t)

		st := storage.Storage{
			Provider: storage.ProviderS3,
			Config: map[string]string{
				"s3_bucket":      "credential-less-bucket",
				"s3_endpoint":    ctrl.Endpoint,
				"s3_donotusetls": "true",
				"s3_access_key":  "explicit-access-key",
				"s3_secret_key":  "explicit-secret-key",
			},
		}

		_, err := s3BlobStorage(ctx, repository.Options{}, st)
		if err != nil {
			assert.NotContains(t, err.Error(), "missing required storage configuration",
				"an explicit-credential config must not be rejected by the credential check")
		}

		assert.NotEmpty(t, ctrl.Requests(), "the recorder must be able to see a request at all")
		assert.True(t, ctrl.SawAuthorization(),
			"an explicit-credential request is signed; that is what the credential-less cases must not do")
	})
}
