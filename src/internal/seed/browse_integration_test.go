package seed

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/engine"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// This file is chunk 0.7's live proof of pkg/engine's browse API against
// the real test tenant: it backs up every workload 0.3 seeds, then walks
// the resulting backup points with engine.ListFolders, engine.ListItems
// and engine.FindItemsByName and checks what it finds against
// seed.LoadManifest - the seeder's own machine-readable record of what is
// in the tenant - rather than against expectations hardcoded here. If the
// seeder's content changes, these tests follow it.
//
// It lives in internal/seed for the same reason 0.6's
// engine_integration_test.go does: it needs this package's
// Config/RequiredEnvVars/MissingVars and the seeded names, and `make
// test-integration` is scoped to ./internal/seed/.... pkg/engine does not
// import this package; only this test file does.
//
// Nothing here writes to Microsoft 365. seed.Run is the same idempotent
// find-or-create call 0.3, 0.5 and 0.6 already proved safe against this
// tenant; everything after it is a read from Graph (through the backup)
// and reads from a local, disposable Kopia repository. No user, group or
// site is created, modified or deleted, and no DELETE is issued against
// anything.
//
// Credentials are read, never logged.

type BrowseIntegrationSuite struct {
	tester.Suite
}

func TestBrowseIntegrationSuite(t *testing.T) {
	suite.Run(t, &BrowseIntegrationSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{RequiredEnvVars},
			tester.CorsoNightlyTests),
	})
}

// exchangeSelector scopes a backup to exactly the three Exchange
// containers the seeder owns - never Inbox, Calendar or the default
// Contacts folder, which it does not own and this test has no business
// reading.
func exchangeSelector(userID string) selectors.Selector {
	seb := selectors.NewExchangeBackup([]string{userID})
	seb.Include(
		seb.MailFolders([]string{mailFolderName}, selectors.PrefixMatch()),
		seb.EventCalendars([]string{calendarName}, selectors.PrefixMatch()),
		seb.ContactFolders([]string{contactsFolderName}, selectors.PrefixMatch()))
	seb.DiscreteOwner = userID

	return seb.Selector
}

// oneDriveSelector scopes a backup to the seeder's own OneDrive folder.
func oneDriveSelector(userID string) selectors.Selector {
	sod := selectors.NewOneDriveBackup([]string{userID})
	sod.Include(sod.Folders([]string{oneDriveFolderName}, selectors.PrefixMatch()))
	sod.DiscreteOwner = userID

	return sod.Selector
}

// sharePointSelector scopes a backup to the seeded site's document
// libraries and the seeder's own list.
//
// Libraries cannot be narrowed to one drive at backup time the way mail
// folders can - the fork's library scopes name folder paths within a
// drive, not the drive itself - so this reads every document library on
// the seeded site. That is a read, and the site is a dedicated test site;
// the assertions below filter down to the seeded library by its drive
// name rather than assuming it is the only one.
func sharePointSelector(siteID string) selectors.Selector {
	ssb := selectors.NewSharePointBackup([]string{siteID})
	ssb.Include(
		ssb.LibraryFolders(selectors.Any()),
		ssb.Lists([]string{listName}, selectors.PrefixMatch()))
	ssb.DiscreteOwner = siteID

	return ssb.Selector
}

// manifestItems indexes a workload's manifest items by kind.
func manifestItems(m Manifest, w Workload) map[string][]ManifestItem {
	out := map[string][]ManifestItem{}

	for _, item := range m.ItemsByWorkload(w) {
		out[item.Kind] = append(out[item.Kind], item)
	}

	return out
}

// itemsByName indexes browse items by name, reporting a duplicate rather
// than silently keeping the last. Duplicates matter here: a cancelled
// recurring occurrence showing up as a second item with the series'
// subject is exactly the kind of thing these tests exist to notice.
func itemsByName(t *testing.T, items []engine.Item) map[string]engine.Item {
	t.Helper()

	out := map[string]engine.Item{}

	for _, it := range items {
		_, dupe := out[it.Name]
		assert.False(t, dupe, "two listed items share the name %q", it.Name)
		out[it.Name] = it
	}

	return out
}

// folderNamed returns the one folder in that category with that display
// name, failing if there is not exactly one.
func folderNamed(t *testing.T, folders []engine.Folder, category, name string) engine.Folder {
	t.Helper()

	var (
		match   engine.Folder
		matches int
	)

	for _, f := range folders {
		if f.Category == category && f.Name == name {
			match = f
			matches++
		}
	}

	require.Equal(t, 1, matches,
		"expected exactly one %s folder named %q; found %d", category, name, matches)

	return match
}

// assertManifestItem checks one browse item against the seeder's own
// manifest entry for it.
//
// Two normalisations, both deliberate and both documented rather than
// papered over:
//
//   - Drive folder paths. The manifest records a drive item's folder path
//     from the drive's root downwards ("k3ep-seed"), while browse reports
//     the tree the backup actually recorded, which starts at the drive's
//     own root element ("root:/k3ep-seed"). Neither is wrong; they are
//     different conventions for the same place, so the root prefix is
//     stripped before comparing.
//   - Timestamps to the second. Both sides read the same Graph
//     createdDateTime/lastModifiedDateTime, but each round-trips through
//     its own JSON encoding (the seed manifest's, and the backup details
//     manifest's) on the way here, so sub-second precision is not
//     guaranteed to survive identically.
func assertManifestItem(t *testing.T, want ManifestItem, got engine.Item) {
	t.Helper()

	assert.Equal(t, want.Name, got.Name)

	gotFolder := strings.TrimPrefix(got.FolderPath, "root:")
	gotFolder = strings.TrimPrefix(gotFolder, "/")
	assert.Equal(t, want.FolderPath, gotFolder,
		"item %q is in the wrong folder", want.Name)

	assert.NotEmpty(t, got.ID, "item %q must carry a stable ID", want.Name)

	if want.SizeBytes > 0 {
		assert.Equal(t, want.SizeBytes, got.SizeBytes,
			"item %q has the wrong size", want.Name)
	}

	if want.CreatedAt != nil {
		assert.WithinDuration(t, want.CreatedAt.UTC(), got.CreatedAt.UTC(), time.Second,
			"item %q has the wrong creation date", want.Name)
	}

	if want.ModifiedAt != nil {
		assert.WithinDuration(t, want.ModifiedAt.UTC(), got.ModifiedAt.UTC(), time.Second,
			"item %q has the wrong modification date", want.Name)
	}
}

// TestBrowse_SeededObjectMatchesManifest is this chunk's headline
// acceptance criterion, live: back up every seeded workload, browse each
// resulting backup point, and check the folder tree and items against
// seed.LoadManifest.
func (suite *BrowseIntegrationSuite) TestBrowse_SeededObjectMatchesManifest() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// Required for this test to pass standalone, not just as part of the
	// whole ./internal/seed/... suite - see engine_integration_test.go's
	// identical call for the full explanation.
	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	manifestPath := filepath.Join(t.TempDir(), "seed-manifest.json")
	cfg := engineTestConfig(t, manifestPath)
	acct := engineTestAccount(t, cfg)

	creds := account.M365Config{
		M365:          credentials.M365{AzureClientID: cfg.ClientID, AzureClientSecret: cfg.ClientSecret},
		AzureTenantID: cfg.TenantID,
	}

	client, err := api.NewClient(creds, control.DefaultOptions(), count.New())
	require.NoError(t, err, clues.ToCore(err))

	// Confirm the seeded content is really there, and write the manifest
	// these assertions read back.
	_, err = Run(ctx, client, cfg, func(line string) { t.Log(line) })
	require.NoError(t, err, clues.ToCore(err))

	manifest, err := LoadManifest(manifestPath)
	require.NoError(t, err, clues.ToCore(err))

	require.Equal(t, ManifestSchemaVersion, manifest.SchemaVersion,
		"these assertions are written against manifest schema version %d", ManifestSchemaVersion)

	repoCfg := engine.RepositoryConfig{
		Storage: filesystemStorage(t, "corso-0.7-browse-"+uuid.NewString()),
		Account: acct,
		Options: control.DefaultOptions(),
	}

	repo, err := engine.InitializeRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, repo.Close(ctx)) }()

	objectTag := "corso-0.7-browse-" + uuid.NewString()

	// One backup per service: a selector covers one service, and the
	// three categories of Exchange come from one Exchange selector.
	points := map[string]engine.BackupPoint{}

	for _, b := range []struct {
		name string
		sel  selectors.Selector
	}{
		{name: "exchange", sel: exchangeSelector(cfg.UserID)},
		{name: "onedrive", sel: oneDriveSelector(cfg.UserID)},
		{name: "sharepoint", sel: sharePointSelector(cfg.SiteID)},
	} {
		point, err := repo.Backup(ctx, engine.BackupRequest{
			Selector: b.sel,
			Tags: map[string]string{
				"k3ep-tenant": cfg.TenantID,
				"k3ep-policy": b.name,
				"k3ep-object": objectTag,
			},
		})
		require.NoError(t, err, "backing up %s: %v", b.name, clues.ToCore(err))

		// Every assertion below browses this point without opting in to
		// incomplete points, so a throttled, partially-uploaded backup
		// must fail loudly here rather than quietly produce a fragment
		// these tests then compare against the manifest.
		require.True(t, point.Type.IsIndependentlyRestorable(),
			"the %s backup produced a %s point, not a complete one; browse assertions "+
				"would be comparing a fragment against the full manifest", b.name, point.Type)

		points[b.name] = point
	}

	suite.Run("ExchangeMailFolderTreeAndItems", func() {
		t := suite.T()

		point := points["exchange"]

		tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		byKind := manifestItems(manifest, WorkloadExchangeMail)
		require.Len(t, byKind["folder"], 1, "the manifest must record one seeded mail folder")

		wantFolder := byKind["folder"][0]
		folder := folderNamed(t, tree.Folders, "email", wantFolder.Name)
		assert.Equal(t, "exchange", folder.Service)
		assert.Empty(t, folder.ParentID, "the seeded mail folder sits at the mailbox root")

		list, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: point.ID,
			FolderID:      folder.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		got := itemsByName(t, list.Items)

		require.NotEmpty(t, byKind["message"], "the manifest must record seeded messages")

		for _, want := range byKind["message"] {
			msg, ok := got[want.Name]
			require.True(t, ok, "seeded message %q is missing from the backup point", want.Name)
			assertManifestItem(t, want, msg)
			assert.Equal(t, folder.ID, msg.FolderID)
			assert.Equal(t, "email", msg.Category)
		}

		assert.Len(t, list.Items, len(byKind["message"]),
			"the folder must hold exactly the seeded messages and nothing else")
	})

	suite.Run("ExchangeCalendarFolderTreeAndItems", func() {
		t := suite.T()

		point := points["exchange"]

		tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		byKind := manifestItems(manifest, WorkloadExchangeCalendar)
		require.Len(t, byKind["calendar"], 1)

		folder := folderNamed(t, tree.Folders, "events", byKind["calendar"][0].Name)
		assert.Equal(t, "exchange", folder.Service)

		list, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: point.ID,
			FolderID:      folder.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		got := itemsByName(t, list.Items)

		for _, want := range byKind["event"] {
			evt, ok := got[want.Name]
			require.True(t, ok, "seeded event %q is missing from the backup point", want.Name)
			assertManifestItem(t, want, evt)
			assert.Equal(t, "events", evt.Category)
		}

		// The recurring series, manifest schema version 3's addition.
		require.Len(t, byKind["event_recurring"], 1,
			"the manifest must record the seeded recurring series")

		series := byKind["event_recurring"][0]
		require.NotNil(t, series.Recurrence,
			"the recurring series' manifest item must carry its Recurrence detail")
		require.Len(t, series.Recurrence.CancelledOccurrenceIDs, 1)
		require.Len(t, series.Recurrence.ExceptionOccurrenceIDs, 1)

		seriesItem, ok := got[series.Name]
		require.True(t, ok, "the recurring series is missing from the backup point")
		assertManifestItem(t, series, seriesItem)

		// A recurring series is exactly ONE browsable item. Its exception
		// occurrence is serialised inside the series master (see
		// api.fixupExceptionOccurrences), and its cancelled occurrence is
		// not returned by Graph at all, so neither is a browsable item of
		// its own. itemsByName above already fails on a duplicate name,
		// which is how a cancelled occurrence leaking through would show
		// up; this pins the exception's absence explicitly.
		require.Len(t, byKind["event_exception"], 1)

		exception := byKind["event_exception"][0]
		require.NotEqual(t, series.Name, exception.Name,
			"this assertion is only meaningful if the exception has its own subject")

		_, exceptionIsListed := got[exception.Name]
		assert.False(t, exceptionIsListed,
			"an exception occurrence is stored inside its series master, so it must not "+
				"list as a separate item; if this now fails, the fork's event handling changed "+
				"and browse's contract needs revisiting, not this assertion relaxing")

		assert.Len(t, list.Items, len(byKind["event"])+1,
			"the calendar must hold exactly the seeded single events plus the one series master")
	})

	suite.Run("ExchangeContactsFolderTreeAndItems", func() {
		t := suite.T()

		point := points["exchange"]

		tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		byKind := manifestItems(manifest, WorkloadExchangeContacts)
		require.Len(t, byKind["folder"], 1)

		folder := folderNamed(t, tree.Folders, "contacts", byKind["folder"][0].Name)
		assert.Equal(t, "exchange", folder.Service)

		list, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: point.ID,
			FolderID:      folder.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		got := itemsByName(t, list.Items)

		require.NotEmpty(t, byKind["contact"])

		for _, want := range byKind["contact"] {
			contact, ok := got[want.Name]
			require.True(t, ok, "seeded contact %q is missing from the backup point", want.Name)
			assertManifestItem(t, want, contact)
			assert.Equal(t, "contacts", contact.Category)
		}

		assert.Len(t, list.Items, len(byKind["contact"]))
	})

	suite.Run("OneDriveFolderTreeAndItems", func() {
		t := suite.T()

		point := points["onedrive"]

		tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		byKind := manifestItems(manifest, WorkloadOneDrive)
		require.Len(t, byKind["folder"], 1, "the manifest must record one seeded OneDrive folder")

		wantFolder := byKind["folder"][0]
		folder := folderNamed(t, tree.Folders, "files", wantFolder.Name)
		assert.Equal(t, "onedrive", folder.Service)
		assert.Equal(t, wantFolder.ResourceID, folder.DriveID,
			"the manifest records the owning drive's ID as the folder's resource")

		// The seeded folder hangs off the drive's own root, which browse
		// reports as a folder of its own.
		root := folderNamed(t, tree.Folders, "files", "root:")
		assert.Equal(t, root.ID, folder.ParentID)
		assert.Empty(t, root.ParentID)

		list, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: point.ID,
			FolderID:      folder.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		got := itemsByName(t, list.Items)

		require.NotEmpty(t, byKind["file"])

		for _, want := range byKind["file"] {
			file, ok := got[want.Name]
			require.True(t, ok, "seeded file %q is missing from the backup point", want.Name)
			assertManifestItem(t, want, file)
			assert.Equal(t, "files", file.Category)
			assert.Equal(t, want.ResourceID, file.DriveID)
		}

		assert.Len(t, list.Items, len(byKind["file"]),
			"the folder must hold exactly the seeded files, with no metadata entries")
	})

	suite.Run("SharePointLibraryAndListFolderTreeAndItems", func() {
		t := suite.T()

		point := points["sharepoint"]

		tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		libraryByKind := manifestItems(manifest, WorkloadSharePointLibrary)
		require.Len(t, libraryByKind["library"], 1,
			"the manifest must record one seeded document library")

		wantLibrary := libraryByKind["library"][0]

		// A document library is a drive, not a folder, so it is named by
		// the DriveName on the folders inside it.
		var libraryRoot engine.Folder

		for _, f := range tree.Folders {
			if f.Category == "libraries" && f.DriveName == wantLibrary.Name && f.Path == "root:" {
				libraryRoot = f
			}
		}

		require.NotEmpty(t, libraryRoot.ID,
			"the seeded document library %q has no root folder in the backup point", wantLibrary.Name)
		assert.Equal(t, "sharepoint", libraryRoot.Service)

		list, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: point.ID,
			FolderID:      libraryRoot.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		got := itemsByName(t, list.Items)

		require.NotEmpty(t, libraryByKind["file"])

		for _, want := range libraryByKind["file"] {
			file, ok := got[want.Name]
			require.True(t, ok, "seeded library file %q is missing", want.Name)
			assertManifestItem(t, want, file)
			assert.Equal(t, "libraries", file.Category)
			assert.Equal(t, wantLibrary.Name, file.DriveName)
		}

		assert.Len(t, list.Items, len(libraryByKind["file"]),
			"the library root must hold exactly the seeded files")

		// The list. Browse reports a whole list as one item carrying its
		// name and a count of the rows inside it, because that is how the
		// fork records one - see engine.Item.ItemCount.
		listByKind := manifestItems(manifest, WorkloadSharePointList)
		require.Len(t, listByKind["list"], 1)

		wantList := listByKind["list"][0]

		everything, err := repo.ListItems(ctx, engine.ItemsRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		var listItem engine.Item

		for _, it := range everything.Items {
			if it.Category == "lists" && it.Name == wantList.Name {
				listItem = it
			}
		}

		require.NotEmpty(t, listItem.ID,
			"the seeded list %q is missing from the backup point", wantList.Name)
		assertManifestItem(t, wantList, listItem)
		assert.Empty(t, listItem.FolderID, "a list sits outside any folder")
		assert.Equal(t, int64(len(listByKind["list_item"])), listItem.ItemCount,
			"the list must report as many rows as the manifest seeded into it")

		// And its rows are not separately browsable: they are inside the
		// one list item above.
		rowNames := map[string]bool{}
		for _, want := range listByKind["list_item"] {
			rowNames[want.Name] = true
		}

		require.NotEmpty(t, rowNames)

		for _, it := range everything.Items {
			assert.False(t, rowNames[it.Name],
				"list row %q must not list as an item of its own; the fork records a whole "+
					"list as a single item (engine.Item.ItemCount)", it.Name)
		}
	})

	// -----------------------------------------------------------------
	// Done when #3: stable IDs, proven across two separate calls, live.
	// -----------------------------------------------------------------

	suite.Run("ItemIDsAreStableAcrossTwoSeparateCalls", func() {
		t := suite.T()

		point := points["exchange"]

		first, err := repo.ListItems(ctx, engine.ItemsRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))
		require.NotEmpty(t, first.Items)

		second, err := repo.ListItems(ctx, engine.ItemsRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		firstIDs := map[string]string{}
		for _, it := range first.Items {
			firstIDs[it.Name] = it.ID
		}

		secondIDs := map[string]string{}
		for _, it := range second.Items {
			secondIDs[it.Name] = it.ID
		}

		assert.Equal(t, firstIDs, secondIDs,
			"a second, separate ListItems call must report the same ID for each item")

		// Stable across a repository reopen too, which is the case that
		// matters: k3ep hands an ID to an admin in one request and acts
		// on it in another, through a different repository handle.
		require.NoError(t, repo.Close(ctx))

		reopened, err := engine.OpenRepository(ctx, repoCfg)
		require.NoError(t, err, clues.ToCore(err))

		third, err := reopened.ListItems(ctx, engine.ItemsRequest{BackupPointID: point.ID})
		require.NoError(t, err, clues.ToCore(err))

		thirdIDs := map[string]string{}
		for _, it := range third.Items {
			thirdIDs[it.Name] = it.ID
		}

		assert.Equal(t, firstIDs, thirdIDs,
			"item IDs must survive closing and reopening the repository")

		require.NoError(t, reopened.Close(ctx))

		// Re-open the handle the deferred Close and later subtests use.
		repo, err = engine.OpenRepository(ctx, repoCfg)
		require.NoError(t, err, clues.ToCore(err))
	})

	// -----------------------------------------------------------------
	// Done when #4: finding by name, with a negative case, live.
	// -----------------------------------------------------------------

	suite.Run("FindItemsByNameMatchesSeededItemsAndExcludesOthers", func() {
		t := suite.T()

		tags := map[string]string{"k3ep-object": objectTag}

		mailItems := manifest.ItemsByWorkload(WorkloadExchangeMail)

		var wantMessage ManifestItem

		for _, item := range mailItems {
			if item.Kind == "message" {
				wantMessage = item
				break
			}
		}

		require.NotEmpty(t, wantMessage.Name, "the manifest must record a seeded message to find")

		// Exact name: finds that message, and nothing else.
		exact, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
			Tags:  tags,
			Name:  wantMessage.Name,
			Match: engine.NameMatchExact,
		})
		require.NoError(t, err, clues.ToCore(err))
		require.Len(t, exact, 1, "an exact name must match exactly the one seeded message")
		assert.Equal(t, wantMessage.Name, exact[0].Item.Name)
		assert.Equal(t, points["exchange"].ID, exact[0].BackupPointID)
		assert.True(t, exact[0].BackupPointIndependentlyRestorable)
		assert.NotEmpty(t, exact[0].Item.ID)

		// The same ID the browse listing gave for that message - a find
		// and a browse must agree, or 5.1 and 7.3 would get different
		// handles depending on how the admin got there.
		folder := folderNamed(t,
			mustListFolders(ctx, t, repo, points["exchange"].ID),
			"email",
			mailFolderName)

		browsed, err := repo.ListItems(ctx, engine.ItemsRequest{
			BackupPointID: points["exchange"].ID,
			FolderID:      folder.ID,
		})
		require.NoError(t, err, clues.ToCore(err))

		assert.Equal(t, itemsByName(t, browsed.Items)[wantMessage.Name].ID, exact[0].Item.ID,
			"finding an item by name and browsing to it must yield the same stable ID")

		// Substring: the seed prefix matches every seeded item across
		// every one of this object's backup points.
		prefixed, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
			Tags: tags,
			Name: Prefix,
		})
		require.NoError(t, err, clues.ToCore(err))
		assert.GreaterOrEqual(t, len(prefixed), len(exact),
			"a substring of the seed prefix must match at least the exact hit")

		for _, f := range prefixed {
			assert.Contains(t, strings.ToLower(f.Item.Name), strings.ToLower(Prefix),
				"a find must not return an item whose name does not match")
		}

		// The negative case: a name no seeded item carries returns
		// nothing, rather than everything or an error.
		none, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
			Tags: tags,
			Name: Prefix + "-no-such-item-" + uuid.NewString(),
		})
		require.NoError(t, err, clues.ToCore(err))
		assert.Empty(t, none, "a name nothing carries must match nothing")

		// And a find scoped to a different object must not reach this
		// object's items at all, even though both live in the same
		// repository - the tag scoping 0.6 established, exercised from
		// browse's side.
		otherObject, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
			Tags: map[string]string{"k3ep-object": "corso-0.7-not-this-object-" + uuid.NewString()},
			Name: wantMessage.Name,
		})
		require.NoError(t, err, clues.ToCore(err))
		assert.Empty(t, otherObject,
			"a find scoped to another object must not return this object's items")
	})

	// -----------------------------------------------------------------
	// The documented refusal, against a real backup point.
	// -----------------------------------------------------------------

	suite.Run("BrowsingAnUnknownPointFails", func() {
		t := suite.T()

		_, err := repo.ListFolders(ctx, engine.FolderTreeRequest{
			BackupPointID: uuid.NewString(),
		})
		assert.ErrorIs(t, err, engine.ErrBackupPointNotFound)

		_, err = repo.ListItems(ctx, engine.ItemsRequest{BackupPointID: uuid.NewString()})
		assert.ErrorIs(t, err, engine.ErrBackupPointNotFound)
	})
}

func mustListFolders(
	ctx context.Context,
	t *testing.T,
	repo *engine.Repository,
	pointID string,
) []engine.Folder {
	t.Helper()

	tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: pointID})
	require.NoError(t, err, clues.ToCore(err))

	return tree.Folders
}
