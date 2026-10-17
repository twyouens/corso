package seed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// This is the harness's one integration smoke test (Done-when #5): it
// proves the seeder genuinely reaches the real test tenant and sees what
// it seeded, and (Done-when #2) that running it twice changes nothing but
// the manifest's timestamp. It is deliberately the *only* integration test
// this chunk adds - the backup/restore integration suites that exercise
// this seeded data belong to chunks 0.5-0.7, not here.
//
// Gated the same way every other integration test in this repository is
// gated (see internal/tester/suite.go): it runs only when CORSO_CI_TESTS or
// CORSO_NIGHTLY_TESTS is set, which is also what keeps it out of `make
// check`. Reading the seeder's own env vars directly here (rather than
// through a library function) follows the same precedent as
// internal/tester/tconfig, which does the same for the rest of this
// repository's integration-test credentials.
type SeedHarnessIntgSuite struct {
	tester.Suite
}

func TestSeedHarnessIntgSuite(t *testing.T) {
	suite.Run(t, &SeedHarnessIntgSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{RequiredEnvVars},
			tester.CorsoNightlyTests),
	})
}

func (suite *SeedHarnessIntgSuite) TestRun_SeedsTenantAndIsIdempotent() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	cfg := Config{
		TenantID:     os.Getenv(EnvTenantID),
		ClientID:     os.Getenv(EnvClientID),
		ClientSecret: os.Getenv(EnvClientSecret),
		UserID:       os.Getenv(EnvSeedUserID),
		SiteID:       os.Getenv(EnvSeedSiteID),
		ManifestPath: filepath.Join(t.TempDir(), "seed-manifest.json"),
	}

	creds := account.M365Config{
		M365:          credentials.M365{AzureClientID: cfg.ClientID, AzureClientSecret: cfg.ClientSecret},
		AzureTenantID: cfg.TenantID,
	}

	client, err := api.NewClient(creds, control.DefaultOptions(), count.New())
	require.NoError(t, err, clues.ToCore(err))

	// First run: seeds the tenant (or finds everything already there from a
	// previous run) and writes the manifest.
	first, err := Run(ctx, client, cfg)
	require.NoError(t, err, clues.ToCore(err))

	requireExpectedWorkloads(t, first)

	// Second run, same config: Done-when #2's live proof. If this seeder
	// is genuinely idempotent, nothing but GeneratedAt differs.
	second, err := Run(ctx, client, cfg)
	require.NoError(t, err, clues.ToCore(err))

	require.Equal(t, first.TenantID, second.TenantID)
	require.Equal(t, first.Prefix, second.Prefix)
	require.Equal(t, first.Items, second.Items,
		"a second seeder run must not create, remove or alter any item")
	require.NotEqual(t, first.GeneratedAt, second.GeneratedAt,
		"GeneratedAt is expected to differ; it is the one field allowed to")

	// And the manifest on disk after the second run matches what Run
	// returned, i.e. "listing the tenant matches the seed manifest" is
	// checkable by reading the file, not just the in-memory value.
	onDisk, err := LoadManifest(cfg.ManifestPath)
	require.NoError(t, err, clues.ToCore(err))
	require.Equal(t, second.Items, onDisk.Items)
}

func requireExpectedWorkloads(t *testing.T, m Manifest) {
	for _, w := range []Workload{
		WorkloadExchangeMail,
		WorkloadExchangeCalendar,
		WorkloadExchangeContacts,
		WorkloadOneDrive,
		WorkloadSharePointLibrary,
		WorkloadSharePointList,
	} {
		require.NotEmpty(t, m.ItemsByWorkload(w), "expected at least one seeded item for workload %q", w)
	}
}
