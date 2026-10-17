package seed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
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

	// Belt-and-braces credential validation (Done-when #7): tester.MustGetEnvSets
	// (called by NewIntegrationSuite above) checks that each env var *name* is a
	// non-empty string literal, not that its *value* is set, so it would not
	// actually catch a missing credential. Check the real thing ourselves and
	// fail with the same clear, name-only message the seed command itself
	// reports, so `make test-integration` with one variable unset behaves the
	// same way `go run ./src/cmd/seed` does.
	if missing := MissingVars(os.Getenv, RequiredEnvVars); len(missing) > 0 {
		t.Fatal(MissingVarsError(missing))
	}

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

	// report makes the resolved target identity and each workload's
	// found-versus-created tally visible in `go test -v`'s output, so
	// anyone re-running this test themselves can check it against what a
	// result report claims, without this test ever logging an item's
	// name, subject, filename or a credential.
	report := func(line string) { t.Log(line) }

	// First run: seeds the tenant (or finds everything already there from a
	// previous run) and writes the manifest.
	t.Log("--- first run ---")

	first, err := Run(ctx, client, cfg, report)
	require.NoError(t, err, clues.ToCore(err))

	requireExpectedWorkloads(t, first)
	requireDatesPopulatedWhereExpected(t, first)
	requireRecurringEventReadsTypedOccurrences(t, first)

	// Second run, same config: Done-when #2's live proof. If this seeder
	// is genuinely idempotent, nothing but GeneratedAt differs.
	t.Log("--- second run ---")

	second, err := Run(ctx, client, cfg, report)
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

// requireDatesPopulatedWhereExpected is the live proof for this chunk's
// must-fix: every manifest item that Graph actually returns a
// createdDateTime for must have a non-zero CreatedAt, whether it was found
// or freshly created. The only items exempt are the three lightweight
// Exchange containers (mail folder, calendar, contacts folder), which
// genuinely have no createdDateTime/lastModifiedDateTime in Graph at all
// (see mail.go/calendar.go/contacts.go's comments on their Entity-only
// models) - everything else, including the messages/events/contacts
// inside them, is expected to carry a real date.
func requireDatesPopulatedWhereExpected(t *testing.T, m Manifest) {
	for _, item := range m.Items {
		if isDatelessContainer(item) {
			continue
		}

		if assert.NotNil(t, item.CreatedAt,
			"%s %s %q should carry a real CreatedAt", item.Workload, item.Kind, item.Name) {
			assert.False(t, item.CreatedAt.IsZero())
		}

		if assert.NotNil(t, item.ModifiedAt,
			"%s %s %q should carry a real ModifiedAt", item.Workload, item.Kind, item.Name) {
			assert.False(t, item.ModifiedAt.IsZero())
		}
	}
}

// requireRecurringEventReadsTypedOccurrences is this chunk's entire point:
// chunk 0.5 upgraded the Graph SDK from v1.30.0 to v1.104.0, promoting
// Event.CancelledOccurrences/ExceptionOccurrences from beta-only
// AdditionalData entries to typed fields, and deleted the old
// api.EventFromMap/parseableToMap map-parsing code as dead. Nothing
// exercised that migration against real Graph data, because every
// seeded event before this chunk was a single non-recurring occurrence -
// the live calendar never contained a cancellation or an exception for
// the typed fields to carry. This assertion reads the one recurring
// series this chunk adds and requires both fields non-empty; if the
// typed-field migration were broken, or ever reverted to reading
// AdditionalData directly, Eventable.GetCancelledOccurrences()/
// GetExceptionOccurrences() would come back empty and this fails.
func requireRecurringEventReadsTypedOccurrences(t *testing.T, m Manifest) {
	var series *ManifestItem

	for i, item := range m.Items {
		if item.Workload == WorkloadExchangeCalendar && item.Kind == "event_recurring" {
			series = &m.Items[i]
			break
		}
	}

	if !assert.NotNil(t, series, "expected a seeded recurring calendar event (Kind %q)", "event_recurring") {
		return
	}

	if !assert.NotNil(t, series.Recurrence,
		"seeded recurring event %q should carry RecurrenceDetail", series.Name) {
		return
	}

	assert.NotEmpty(t, series.Recurrence.CancelledOccurrenceIDs,
		"Eventable.GetCancelledOccurrences() should be non-empty - "+
			"the typed field the Graph SDK v1.104.0 upgrade promoted this out of AdditionalData")
	assert.NotEmpty(t, series.Recurrence.ExceptionOccurrenceIDs,
		"Eventable.GetExceptionOccurrences() should be non-empty - "+
			"the typed field the Graph SDK v1.104.0 upgrade promoted this out of AdditionalData")

	// The exception occurrence should also be present as its own item,
	// not just referenced by ID in RecurrenceDetail.
	found := false

	for _, item := range m.Items {
		if item.Workload == WorkloadExchangeCalendar && item.Kind == "event_exception" {
			found = true
			break
		}
	}

	assert.True(t, found, "expected the seeded exception occurrence as its own manifest item (Kind %q)", "event_exception")
}

func isDatelessContainer(item ManifestItem) bool {
	switch item.Workload {
	case WorkloadExchangeMail:
		return item.Kind == "folder"
	case WorkloadExchangeCalendar:
		return item.Kind == "calendar"
	case WorkloadExchangeContacts:
		return item.Kind == "folder"
	default:
		return false
	}
}
