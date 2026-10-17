package kopia

import (
	"net/http"
	"testing"
	"time"

	"github.com/kopia/kopia/repo/blob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
)

type S3TLSHandshakeTimeoutUnitSuite struct {
	tester.Suite
}

func TestS3TLSHandshakeTimeoutUnitSuite(t *testing.T) {
	suite.Run(t, &S3TLSHandshakeTimeoutUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// TestAppliesDuringCall proves chunk 0.4's "done when" #7: the S3 TLS
// handshake timeout the Alcion fork used to carry as a field on
// s3.Options (unconditionally set to 60s; see docs/kopia-fork-audit.md
// section 3) is still applied after the upgrade to upstream Kopia, which
// has no such field. withS3TLSHandshakeTimeout re-establishes it locally by
// swapping http.DefaultTransport (the only thing upstream's
// getCustomTransport clones from) for the duration of the call.
func (suite *S3TLSHandshakeTimeoutUnitSuite) TestAppliesDuringCall() {
	t := suite.T()

	original := http.DefaultTransport
	originalTransport, ok := original.(*http.Transport)
	require.True(t, ok, "test assumes the stdlib default *http.Transport")
	originalTimeout := originalTransport.TLSHandshakeTimeout

	var observedDuringCall time.Duration

	_, err := withS3TLSHandshakeTimeout(func() (blob.Storage, error) {
		inUse, ok := http.DefaultTransport.(*http.Transport)
		require.True(t, ok)

		observedDuringCall = inUse.TLSHandshakeTimeout

		return nil, nil
	})
	require.NoError(t, err)

	assert.Equal(t, s3TLSHandshakeTimeout, observedDuringCall, "timeout applied during the call")
	assert.NotEqual(t, originalTimeout, observedDuringCall, "fixture assumption: default differs from the fork's value")
}

// TestRestoresAfterCall proves the swap performed by
// withS3TLSHandshakeTimeout does not leak: once the call returns,
// http.DefaultTransport is back to exactly what it was before, including
// on an error return from fn.
func (suite *S3TLSHandshakeTimeoutUnitSuite) TestRestoresAfterCall() {
	t := suite.T()

	before := http.DefaultTransport

	_, err := withS3TLSHandshakeTimeout(func() (blob.Storage, error) {
		return nil, assert.AnError
	})
	assert.ErrorIs(t, err, assert.AnError)

	assert.Same(t, before, http.DefaultTransport, "http.DefaultTransport restored after an error return")
}
