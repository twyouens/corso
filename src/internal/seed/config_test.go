package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
)

type ConfigUnitSuite struct {
	tester.Suite
}

func TestConfigUnitSuite(t *testing.T) {
	suite.Run(t, &ConfigUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *ConfigUnitSuite) TestMissingVars_AllPresent() {
	t := suite.T()

	values := map[string]string{
		EnvTenantID:     "tenant",
		EnvClientID:     "client",
		EnvClientSecret: "secret",
		EnvSeedUserID:   "user",
		EnvSeedSiteID:   "site",
	}

	get := func(name string) string { return values[name] }

	missing := MissingVars(get, RequiredEnvVars)
	assert.Empty(t, missing)
	assert.NoError(t, MissingVarsError(missing))
}

func (suite *ConfigUnitSuite) TestMissingVars_ReportsOnlyMissingNames() {
	t := suite.T()

	values := map[string]string{
		EnvTenantID:     "tenant",
		EnvClientID:     "",
		EnvClientSecret: "secret",
		EnvSeedUserID:   "",
		EnvSeedSiteID:   "site",
	}

	get := func(name string) string { return values[name] }

	missing := MissingVars(get, RequiredEnvVars)
	assert.ElementsMatch(t, []string{EnvClientID, EnvSeedUserID}, missing)

	err := MissingVarsError(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvClientID)
	assert.Contains(t, err.Error(), EnvSeedUserID)
	// the point of this check: the error names the missing vars, never a value.
	assert.NotContains(t, err.Error(), "tenant")
	assert.NotContains(t, err.Error(), "secret")
}

func (suite *ConfigUnitSuite) TestMissingVars_NeverLeaksAPresentValue() {
	t := suite.T()

	err := MissingVarsError(MissingVars(
		func(string) string { return "" },
		[]string{EnvClientSecret}))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-value")
}
