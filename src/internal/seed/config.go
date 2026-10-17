// Package seed implements the test-tenant seeding harness for chunk 0.3: it
// fills a Microsoft 365 test tenant with known, recognisably-prefixed data
// across the v1 workloads (Exchange mail, calendar, contacts; OneDrive;
// SharePoint document libraries and lists) and records exactly what it
// created in a seed manifest that later chunks assert against.
//
// This package is a library in the sense that it takes all configuration as
// explicit arguments and never reads the environment itself (see config.go);
// the one exception is test setup code in _test.go files, which follows the
// same pattern already used by internal/tester/tconfig for integration-test
// credentials. Only src/cmd/seed (a program, not library code) reads the
// process environment and builds a Config from it.
package seed

import (
	"fmt"

	"github.com/alcionai/clues"
)

// Prefix identifies every object this package creates. It must appear in
// every container and item name so a human auditing the tenant can find
// and, if ever necessary, remove everything the harness owns by hand.
const Prefix = "k3ep-seed"

// Environment variable names the harness (src/cmd/seed and its integration
// test) requires. Centralised here, as plain string constants, so the
// seeder, the integration smoke test, the nightly workflow and docs all
// agree on one set of names. Defining the names here does not violate the
// "library never reads env vars" rule: no os.Getenv call lives in this
// package outside of _test.go files.
const (
	// EnvTenantID, EnvClientID and EnvClientSecret are the application's
	// credentials for the test tenant. These match the fork's CLAUDE.md.
	EnvTenantID     = "AZURE_TENANT_ID"
	EnvClientID     = "AZURE_CLIENT_ID"
	EnvClientSecret = "AZURE_CLIENT_SECRET"

	// EnvSeedUserID names an existing, already-licensed Microsoft 365 user
	// (UPN or object ID) whose mailbox and OneDrive the seeder writes
	// dedicated, Prefix-named folders into. The harness never creates this
	// user: it must already exist in the test tenant.
	EnvSeedUserID = "SEED_USER_ID"

	// EnvSeedSiteID names an existing SharePoint site (canonical Graph site
	// ID, e.g. "contoso.sharepoint.com,<site-guid>,<web-guid>") that the
	// seeder adds a dedicated, Prefix-named document library and list to.
	// The harness never creates this site: it must already exist.
	EnvSeedSiteID = "SEED_SITE_ID"

	// EnvManifestPath overrides where the seeder writes the seed manifest.
	// See manifest.go for the schema. Defaults to DefaultManifestPath.
	EnvManifestPath = "SEED_MANIFEST_PATH"
)

// DefaultManifestPath is used when EnvManifestPath is unset. It is relative
// to the process's working directory, which by convention is the corso
// repository root (see docs/test-tenant-harness.md for the exact invocation).
const DefaultManifestPath = "seed-manifest.json"

// RequiredEnvVars are the env vars that must be set (to a non-empty value)
// before the seeder, or its integration test, can run. Credential validation
// (Done-when #7 of this chunk) checks every name in this list.
var RequiredEnvVars = []string{
	EnvTenantID,
	EnvClientID,
	EnvClientSecret,
	EnvSeedUserID,
	EnvSeedSiteID,
}

// Config carries everything the seeder needs, gathered by the caller
// (src/cmd/seed/main.go, or a test's setup code) from the environment.
// Nothing in this package reads these values from anywhere else.
type Config struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	UserID       string
	SiteID       string
	ManifestPath string
}

// MissingVars reports the names, in RequiredEnvVars order, of every env var
// in names whose value (as returned by get) is empty. It never receives or
// returns a value, only names: the caller passes a lookup function so this
// stays a pure, testable check with no direct access to the process
// environment, and so that whatever calls it can log the result without
// risk of accidentally logging a secret.
func MissingVars(get func(string) string, names []string) []string {
	var missing []string

	for _, name := range names {
		if get(name) == "" {
			missing = append(missing, name)
		}
	}

	return missing
}

// MissingVarsError formats the result of MissingVars into the clear,
// name-only message the harness reports on start-up (Done-when #7): it
// never includes a value, only the variable names that are missing.
func MissingVarsError(missing []string) error {
	if len(missing) == 0 {
		return nil
	}

	return clues.New(fmt.Sprintf("missing required environment variable(s): %v", missing))
}
