package testdata

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// S3RequestRecorder is a loopback HTTP server standing in for an
// S3-compatible endpoint, used to prove a negative: that a given code
// path makes no request at all to a caller-named endpoint, and signs
// nothing.
//
// It exists because of chunk 0.6's security review, which proved that an
// S3 configuration with no credentials fell through Kopia's AWS SDK
// credential chain to the environment and then to the EC2/ECS instance
// role, and serviced a caller-chosen bucket, prefix and endpoint with
// the platform's own identity - measured live as 13 signed requests. The
// tests that use this recorder re-run that attack and assert it yields
// nothing: zero requests, and no Authorization header ever emitted.
//
// Every request it does receive is answered 500, so a path that somehow
// reaches it fails rather than appearing to work.
type S3RequestRecorder struct {
	// Endpoint is the host:port to put in an S3Config's Endpoint field.
	// Pair it with DoNotUseTLS: true.
	Endpoint string

	srv *httptest.Server

	mu            sync.Mutex
	requests      []string
	authorization bool
}

// NewS3RequestRecorder starts a recorder on 127.0.0.1 and stops it when
// the test ends.
func NewS3RequestRecorder(t *testing.T) *S3RequestRecorder {
	rec := &S3RequestRecorder{}

	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.requests = append(rec.requests, r.Method+" "+r.URL.Path)

		if len(r.Header.Get("Authorization")) > 0 {
			// Deliberately records only that the header was present.
			// Its value is a signature derived from a credential and
			// never belongs in a test log.
			rec.authorization = true
		}

		rec.mu.Unlock()

		w.WriteHeader(http.StatusInternalServerError)
	}))

	t.Cleanup(rec.srv.Close)

	rec.Endpoint = rec.srv.Listener.Addr().String()

	return rec
}

// Requests returns the method and path of every request received, in
// order. Never any header value or body.
func (rec *S3RequestRecorder) Requests() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	return append([]string(nil), rec.requests...)
}

// SawAuthorization reports whether any request carried an Authorization
// header - that is, whether anything signed a request with any
// credential at all.
func (rec *S3RequestRecorder) SawAuthorization() bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	return rec.authorization
}
