package account

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccount_NeverLeaksConfig proves Account's own String/GoString/
// MarshalJSON redact Config under every channel a caller of this package
// (not just pkg/engine, which has its own, wider test covering the
// RepositoryConfig shape) might use it standalone.
func TestAccount_NeverLeaksConfig(t *testing.T) {
	const secret = "super-secret-azure-client-secret"

	a := Account{
		Provider: ProviderM365,
		Config:   map[string]string{"azure_clientSecret": secret},
	}

	//lint:ignore S1025 deliberately exercising the %s verb through fmt.Sprintf, not calling String() directly
	sVerbOutput := fmt.Sprintf("%s", a) //nolint:staticcheck // deliberately exercising the %s verb through fmt.Sprintf

	outputs := map[string]string{
		"%v":  fmt.Sprintf("%v", a),
		"%+v": fmt.Sprintf("%+v", a),
		"%#v": fmt.Sprintf("%#v", a),
		"%s":  sVerbOutput,
	}

	jsonOut, err := json.Marshal(a)
	require.NoError(t, err)

	outputs["json"] = string(jsonOut)

	for label, out := range outputs {
		assert.NotContains(t, out, secret, "%s leaked Config", label)
	}

	// sanity: the raw map really does carry the secret.
	assert.Contains(t, fmt.Sprintf("%v", a.Config), secret)
}
