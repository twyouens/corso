package seed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/fault"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// This is chunk 0.5's live proof that the Graph SDK, Kiota and azidentity
// upgrade did not silently break the clients in pkg/services/m365/api that
// 0.6 and 0.7 build on: it re-seeds (idempotently; see smoke_test.go) to get
// a fresh manifest, then fetches each Exchange mail/calendar/contact item,
// OneDrive file and SharePoint library file/list item it names by the exact
// client methods the engine uses, asserting each fetch succeeds and returns
// the same ID the manifest recorded.
//
// This specifically exercises Events().GetItem's typed
// CancelledOccurrences/ExceptionOccurrences migration (see CHANGELOG.md)
// against a real Graph response, not just the mocked fixtures in
// internal/m365/collection/exchange/transform_test.go - the seeded calendar
// event is not recurring, so those two fields are expected to come back
// empty, but the call path (including fixupExceptionOccurrences and
// GetCancelledEventDateStrings) still runs for real.
//
// Lives in this package, not pkg/services/m365/api, specifically to avoid
// an import cycle: pkg/services/m365/api cannot import this package (it
// already imports pkg/services/m365/api itself), but this package importing
// pkg/services/m365/api back is exactly the existing, accepted direction
// every other file here already uses. It runs under the same
// `make test-integration` target as TestSeedHarnessIntgSuite above, with no
// Makefile change needed, since both live under ./internal/seed/....
type GraphClientIntegrationSuite struct {
	tester.Suite
}

func TestGraphClientIntegrationSuite(t *testing.T) {
	suite.Run(t, &GraphClientIntegrationSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{RequiredEnvVars},
			tester.CorsoNightlyTests),
	})
}

func (suite *GraphClientIntegrationSuite) TestAPIClientsReadSeededData() {
	t := suite.T()

	if missing := MissingVars(os.Getenv, RequiredEnvVars); len(missing) > 0 {
		t.Fatal(MissingVarsError(missing))
	}

	ctx, flush := tester.NewContext(t)
	defer flush()

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

	// Run is idempotent find-before-create (see smoke_test.go); every item
	// named here is expected to already exist from a previous seeding run,
	// so this call makes no writes, only reads and (if this is ever run
	// against a genuinely fresh tenant) the same creates the seeder itself
	// already proves safe.
	manifest, err := Run(ctx, client, cfg, func(line string) { t.Log(line) })
	require.NoError(t, err, clues.ToCore(err))

	suite.Run("exchange mail", func() {
		for _, item := range manifest.ItemsByWorkload(WorkloadExchangeMail) {
			if item.Kind != "message" {
				continue
			}

			parsed, _, err := client.Mail().GetItem(ctx, cfg.UserID, item.ID, fault.New(true))
			if !assert.NoError(t, err, "fetching seeded mail item %q", item.Name) {
				continue
			}

			msg, ok := parsed.(models.Messageable)
			require.True(t, ok, "expected a Messageable")
			assert.Equal(t, item.ID, ptr.Val(msg.GetId()))
		}
	})

	suite.Run("exchange calendar", func() {
		for _, item := range manifest.ItemsByWorkload(WorkloadExchangeCalendar) {
			if item.Kind != "event" {
				continue
			}

			parsed, _, err := client.Events().GetItem(ctx, cfg.UserID, item.ID, fault.New(true))
			if !assert.NoError(t, err, "fetching seeded calendar event %q", item.Name) {
				continue
			}

			evt, ok := parsed.(models.Eventable)
			require.True(t, ok, "expected an Eventable")
			assert.Equal(t, item.ID, ptr.Val(evt.GetId()))
		}
	})

	suite.Run("exchange contacts", func() {
		for _, item := range manifest.ItemsByWorkload(WorkloadExchangeContacts) {
			if item.Kind != "contact" {
				continue
			}

			parsed, _, err := client.Contacts().GetItem(ctx, cfg.UserID, item.ID, fault.New(true))
			if !assert.NoError(t, err, "fetching seeded contact %q", item.Name) {
				continue
			}

			cont, ok := parsed.(models.Contactable)
			require.True(t, ok, "expected a Contactable")
			assert.Equal(t, item.ID, ptr.Val(cont.GetId()))
		}
	})

	suite.Run("onedrive", func() {
		for _, item := range manifest.ItemsByWorkload(WorkloadOneDrive) {
			if item.Kind != "file" {
				continue
			}

			di, err := client.Drives().GetItem(ctx, item.ResourceID, item.ID)
			if !assert.NoError(t, err, "fetching seeded OneDrive file %q", item.Name) {
				continue
			}

			assert.Equal(t, item.ID, ptr.Val(di.GetId()))
			assert.Equal(t, item.Name, ptr.Val(di.GetName()))
		}
	})

	suite.Run("sharepoint library", func() {
		libraryItems := manifest.ItemsByWorkload(WorkloadSharePointLibrary)

		var driveID string

		for _, item := range libraryItems {
			if item.Kind == "library" {
				driveID = item.ID
			}
		}

		require.NotEmpty(t, driveID, "manifest must record the seed library's own drive ID")

		for _, item := range libraryItems {
			if item.Kind != "file" {
				continue
			}

			di, err := client.Drives().GetItem(ctx, driveID, item.ID)
			if !assert.NoError(t, err, "fetching seeded SharePoint library file %q", item.Name) {
				continue
			}

			assert.Equal(t, item.ID, ptr.Val(di.GetId()))
			assert.Equal(t, item.Name, ptr.Val(di.GetName()))
		}
	})

	suite.Run("sharepoint list", func() {
		listItems := manifest.ItemsByWorkload(WorkloadSharePointList)

		var listID string

		for _, item := range listItems {
			if item.Kind == "list" {
				listID = item.ID
			}
		}

		require.NotEmpty(t, listID, "manifest must record the seed list's own ID")

		list, _, err := client.Lists().GetListByID(ctx, cfg.SiteID, listID)
		require.NoError(t, err, clues.ToCore(err))

		fetchedIDs := map[string]bool{}

		for _, li := range list.GetItems() {
			fetchedIDs[ptr.Val(li.GetId())] = true
		}

		for _, item := range listItems {
			if item.Kind != "list_item" {
				continue
			}

			assert.True(
				t,
				fetchedIDs[item.ID],
				"seeded list item %q (id %s) must appear in the fetched list",
				item.Name,
				item.ID)
		}
	})
}
