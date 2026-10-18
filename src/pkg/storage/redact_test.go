package storage

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStorage_NeverLeaksConfig proves Storage's own String/GoString/
// MarshalJSON redact Config under every channel a caller of this package
// (not just pkg/engine, which has its own, wider test covering the
// RepositoryConfig shape) might use it standalone.
func TestStorage_NeverLeaksConfig(t *testing.T) {
	const secret = "super-secret-corso-passphrase"

	s := Storage{
		Provider: ProviderS3,
		Config:   map[string]string{"common_corsoPassphrase": secret, "s3_access_key": secret},
	}

	//lint:ignore S1025 deliberately exercising the %s verb through fmt.Sprintf, not calling String() directly
	sVerbOutput := fmt.Sprintf("%s", s) //nolint:staticcheck // deliberately exercising the %s verb through fmt.Sprintf

	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", s),
		"%+v":  fmt.Sprintf("%+v", s),
		"%#v":  fmt.Sprintf("%#v", s),
		"%s":   sVerbOutput,
		"json": mustJSON(t, s),
	}

	for label, out := range outputs {
		assert.NotContains(t, out, secret, "%s leaked Config", label)
	}

	// sanity: the raw map really does carry the secret.
	assert.Contains(t, fmt.Sprintf("%v", s.Config), secret)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return string(b)
}
