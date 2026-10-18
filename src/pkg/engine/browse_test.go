package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/version"
	"github.com/twyouens/corso/src/pkg/backup"
	"github.com/twyouens/corso/src/pkg/backup/details"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/repository"
)

// These tests build a details manifest with the fork's own
// details.Builder - the same type a real backup writes its manifest with
// - so the entry shapes browse parses (a folder entry's location holding
// its *parent's* path, a drive's "root:" element, a SharePoint list
// having no folder entry at all, the nondeterministic folder ordering)
// are the real ones rather than hand-written fixtures that could drift
// from what the fork actually produces.
//
// The live equivalents, asserting against the real test tenant and 0.3's
// seed manifest, are in internal/seed/browse_integration_test.go.

const (
	testTenant = "test-tenant-id"
	testUser   = "test-user-id"
	testSite   = "test-site-id"
	testDrive  = "test-drive-id"
)

var (
	mailCreated  = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	mailModified = time.Date(2026, 3, 2, 10, 30, 0, 0, time.UTC)
	fileCreated  = time.Date(2026, 4, 5, 8, 0, 0, 0, time.UTC)
	fileModified = time.Date(2026, 4, 6, 11, 15, 0, 0, time.UTC)
)

// addMail appends one Exchange mail message, in a mail folder named
// folder, to b.
func addMail(t *testing.T, b *details.Builder, folder, itemID, subject string, size int64) {
	t.Helper()

	rr, err := path.Build(testTenant, testUser, path.ExchangeService, path.EmailCategory, true,
		folder+"-id", itemID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append(folder),
		details.ItemInfo{
			Exchange: &details.ExchangeInfo{
				ItemType: details.ExchangeMail,
				Subject:  subject,
				Size:     size,
				Created:  mailCreated,
				Modified: mailModified,
			},
		}))
}

// addEvent appends one Exchange calendar event, in a calendar named
// calendar, to b.
func addEvent(t *testing.T, b *details.Builder, calendar, itemID, subject string) {
	t.Helper()

	rr, err := path.Build(testTenant, testUser, path.ExchangeService, path.EventsCategory, true,
		calendar+"-id", itemID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append(calendar),
		details.ItemInfo{
			Exchange: &details.ExchangeInfo{
				ItemType: details.ExchangeEvent,
				Subject:  subject,
				Created:  mailCreated,
				Modified: mailModified,
			},
		}))
}

// addContact appends one Exchange contact, in a contacts folder named
// folder, to b. Contacts carry their name in ContactName, not Subject -
// one of the reasons browse needs its own name accessor.
func addContact(t *testing.T, b *details.Builder, folder, itemID, name string) {
	t.Helper()

	rr, err := path.Build(testTenant, testUser, path.ExchangeService, path.ContactsCategory, true,
		folder+"-id", itemID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append(folder),
		details.ItemInfo{
			Exchange: &details.ExchangeInfo{
				ItemType:    details.ExchangeContact,
				ContactName: name,
				Created:     mailCreated,
				Modified:    mailModified,
			},
		}))
}

// addDriveFile appends one OneDrive file, in the drive folder path
// folders (beneath the drive's own "root:"), to b. isMeta marks it as
// one of the fork's permission sidecar entries, which browse must never
// return as an item.
func addDriveFile(
	t *testing.T,
	b *details.Builder,
	itemID, name string,
	size int64,
	isMeta bool,
	folders ...string,
) {
	t.Helper()

	repoElems := append([]string{"drives", testDrive, "root:"}, folders...)
	repoElems = append(repoElems, itemID)

	rr, err := path.Build(testTenant, testUser, path.OneDriveService, path.FilesCategory, true,
		repoElems...)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append("root:").Append(folders...),
		details.ItemInfo{
			OneDrive: &details.OneDriveInfo{
				ItemType:  details.OneDriveItem,
				ItemName:  name,
				Size:      size,
				IsMeta:    isMeta,
				DriveID:   testDrive,
				DriveName: "OneDrive",
				Created:   fileCreated,
				Modified:  fileModified,
			},
		}))
}

// addLibraryFile appends one SharePoint document library file to b. A
// document library is a drive, so its own name lands in DriveName rather
// than in any folder entry.
func addLibraryFile(t *testing.T, b *details.Builder, library, itemID, name string, size int64) {
	t.Helper()

	rr, err := path.Build(testTenant, testSite, path.SharePointService, path.LibrariesCategory, true,
		"drives", library+"-id", "root:", itemID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append("root:"),
		details.ItemInfo{
			SharePoint: &details.SharePointInfo{
				ItemType:  details.SharePointLibrary,
				ItemName:  name,
				Size:      size,
				DriveID:   library + "-id",
				DriveName: library,
				Created:   fileCreated,
				Modified:  fileModified,
			},
		}))
}

// addList appends one SharePoint list to b.
//
// The shape here is the one a real backup produces, confirmed against the
// test tenant: a list sits in a container whose recorded location is the
// list's own Microsoft 365 ID (the fork records no display name for it),
// and the list itself is a single item carrying the title under List -
// not ItemName - plus a count of the rows inside it. The rows are not
// separate entries.
func addList(t *testing.T, b *details.Builder, listID, name string, itemCount int64) {
	t.Helper()

	rr, err := path.Build(testTenant, testSite, path.SharePointService, path.ListsCategory, true,
		listID, listID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		path.Builder{}.Append(listID),
		details.ItemInfo{
			SharePoint: &details.SharePointInfo{
				ItemType: details.SharePointList,
				List:     &details.ListInfo{Name: name, ItemCount: itemCount},
				Created:  fileCreated,
				Modified: fileModified,
			},
		}))
}

// addLocationlessItem appends an item the backup recorded with no
// location at all. No v1 workload produces one - verified live - but
// browse must report it rather than drop it or panic, so this exists to
// cover Item.FolderID's documented "" case.
func addLocationlessItem(t *testing.T, b *details.Builder, itemID, name string) {
	t.Helper()

	rr, err := path.Build(testTenant, testSite, path.SharePointService, path.ListsCategory, true,
		itemID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		&path.Builder{},
		details.ItemInfo{
			SharePoint: &details.SharePointInfo{
				ItemType: details.SharePointList,
				List:     &details.ListInfo{Name: name},
			},
		}))
}

// seededDetails is a manifest covering every workload 0.3 seeds, named
// the same way, so these unit tests exercise the same shapes the live
// tests do.
func seededDetails(t *testing.T) *details.Builder {
	t.Helper()

	b := &details.Builder{}

	addMail(t, b, "k3ep-seed-mail", "msg-001", "k3ep-seed-mail-001", 1024)
	addMail(t, b, "k3ep-seed-mail", "msg-002", "k3ep-seed-mail-002", 2048)
	addEvent(t, b, "k3ep-seed-calendar", "evt-001", "k3ep-seed-event-001")
	addContact(t, b, "k3ep-seed-contacts", "con-001", "k3ep-seed-contact-001")
	addDriveFile(t, b, "file-001", "k3ep-seed-file-001.txt", 34, false, "k3ep-seed")
	addDriveFile(t, b, "file-002", "k3ep-seed-file-002.txt", 52, false, "k3ep-seed")
	addLibraryFile(t, b, "k3ep-seed-library", "doc-001", "k3ep-seed-doc-001.txt", 40)
	addList(t, b, "list-0000-1111", "k3ep-seed-list", 2)

	return b
}

// repoWithDetails builds an engine.Repository over the fake whose single
// backup point carries deets, classified as pointType.
func repoWithDetails(
	t *testing.T,
	deets *details.Details,
	pointType string,
) (*Repository, string, *fakeRepo) {
	t.Helper()

	id := uuid.NewString()

	b := &backup.Backup{
		BaseModel: model.BaseModel{
			ID:   model.StableID(id),
			Tags: map[string]string{model.BackupTypeTag: pointType, "k3ep-object": testUser},
		},
		CreationTime: time.Now().UTC(),
		Version:      version.Backup,
	}

	fr := &fakeRepo{
		backups: []*backup.Backup{b},
		deets:   map[string]*details.Details{id: deets},
	}

	return &Repository{repo: fr}, id, fr
}

// folderByPath finds the one folder with that display path, or fails.
func folderByPath(t *testing.T, folders []Folder, p string) Folder {
	t.Helper()

	for _, f := range folders {
		if f.Path == p {
			return f
		}
	}

	t.Fatalf("no folder with path %q in %d folders", p, len(folders))

	return Folder{}
}

func folderPaths(folders []Folder) []string {
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		out = append(out, f.Path)
	}

	return out
}

func itemNames(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}

	return out
}

// ---------------------------------------------------------------------------
// Done when #2: the folder tree for every seeded workload.
// ---------------------------------------------------------------------------

// TestListFolders_TreeCoversEveryWorkload proves ListFolders returns one
// folder per container the backup recorded, for Exchange mail, calendar
// and contacts, OneDrive, and a SharePoint document library - and that
// Category is what distinguishes a mail folder from a calendar from a
// contacts folder, since nothing in their names does.
func TestListFolders_TreeCoversEveryWorkload(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, id, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)

	assert.Equal(t, id, tree.BackupPointID)
	assert.Equal(t, BackupPointTypeMerge, tree.PointType)
	assert.True(t, tree.IndependentlyRestorable)

	// Exchange: three sibling top-level folders, one per category. A
	// SharePoint list contributes no folder at all.
	assert.Equal(
		t,
		[]string{
			"k3ep-seed-calendar",
			"k3ep-seed-contacts",
			"k3ep-seed-mail",
			"list-0000-1111", // a list's container is named by its list ID
			"root:",
			"root:",
			"root:/k3ep-seed",
		},
		folderPaths(tree.Folders),
		"folder paths must be the recorded tree, deterministically ordered")

	mail := folderByPath(t, tree.Folders, "k3ep-seed-mail")
	assert.Equal(t, "k3ep-seed-mail", mail.Name)
	assert.Equal(t, "exchange", mail.Service)
	assert.Equal(t, "email", mail.Category)
	assert.Empty(t, mail.ParentID, "a top-level folder has no parent")
	assert.Equal(t, int64(3072), mail.SizeBytes, "folder size rolls up its items")
	assert.Empty(t, mail.DriveID, "Exchange folders are not drive-backed")

	cal := folderByPath(t, tree.Folders, "k3ep-seed-calendar")
	assert.Equal(t, "events", cal.Category)
	assert.Equal(t, "exchange", cal.Service)

	con := folderByPath(t, tree.Folders, "k3ep-seed-contacts")
	assert.Equal(t, "contacts", con.Category)
	assert.Equal(t, "exchange", con.Service)

	// OneDrive: the drive's root and the seeded folder beneath it, with
	// the child's ParentID wired to the root's ID.
	var odRoot, spRoot Folder

	for _, f := range tree.Folders {
		if f.Path != "root:" {
			continue
		}

		if f.Service == "onedrive" {
			odRoot = f
		} else {
			spRoot = f
		}
	}

	require.NotEmpty(t, odRoot.ID, "expected a onedrive root: folder")
	assert.Equal(t, "files", odRoot.Category)
	assert.Equal(t, testDrive, odRoot.DriveID)
	assert.Equal(t, "OneDrive", odRoot.DriveName)
	assert.Empty(t, odRoot.ParentID)

	odFolder := folderByPath(t, tree.Folders, "root:/k3ep-seed")
	assert.Equal(t, "k3ep-seed", odFolder.Name)
	assert.Equal(t, odRoot.ID, odFolder.ParentID,
		"the seeded OneDrive folder must hang off the drive's own root")
	assert.Equal(t, int64(86), odFolder.SizeBytes)

	// SharePoint document library: a drive, so it is named in DriveName
	// rather than being a folder of its own.
	require.NotEmpty(t, spRoot.ID, "expected a sharepoint root: folder")
	assert.Equal(t, "sharepoint", spRoot.Service)
	assert.Equal(t, "libraries", spRoot.Category)
	assert.Equal(t, "k3ep-seed-library", spRoot.DriveName,
		"the document library's own name is the drive name")
	assert.Equal(t, "k3ep-seed-library-id", spRoot.DriveID)

	// Every folder ID must be distinct: two different folders sharing an
	// ID would make ListItems' FolderID filter ambiguous.
	seen := map[string]bool{}

	for _, f := range tree.Folders {
		assert.False(t, seen[f.ID], "duplicate folder ID %s", f.ID)
		seen[f.ID] = true
	}
}

// TestListFolders_OrderIsDeterministicAcrossCalls proves the folder
// ordering browse returns does not vary between calls.
//
// This is not defensive padding: the fork emits folder entries from
// maps.Values over a Go map (details.Builder.Details), whose iteration
// order is randomised per range. Reading the same manifest twice
// genuinely yields different entry orders, so without the sort in
// browseEntries this test fails - verified by removing it.
func TestListFolders_OrderIsDeterministicAcrossCalls(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := seededDetails(t)

	// Many separate Details() calls, each re-ranging the folder map,
	// which is where the fork's nondeterminism comes from. One pair would
	// agree by luck often enough to be a weak test: six folder entries in
	// a randomised order coincide roughly one time in a few hundred, so a
	// single comparison would pass for the wrong reason now and then.
	const rounds = 12

	var want []string

	for i := 0; i < rounds; i++ {
		r, id, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

		tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
		require.NoError(t, err)

		got := folderPaths(tree.Folders)

		if i == 0 {
			want = got
			continue
		}

		require.Equal(t, want, got, "folder order differed on round %d", i)
	}
}

// TestListFolders_TwoDrivesDoNotShareARoot is the regression test for a
// real bug this chunk's own tree-building hit: every drive's folder tree
// starts at an element literally named "root:", so keying folders by
// their recorded location alone merged two different drives' roots into
// one node and attached one drive's folders to the other drive's root.
//
// A site with two document libraries is the ordinary case, not a
// contrived one. Without folderKey's drive-ID component this test fails:
// the two roots collapse to one folder and one of the two files is
// reported in the wrong library.
func TestListFolders_TwoDrivesDoNotShareARoot(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addLibraryFile(t, b, "library-one", "doc-001", "one.txt", 10)
	addLibraryFile(t, b, "library-two", "doc-002", "two.txt", 20)

	r, id, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)

	require.Len(t, tree.Folders, 2, "each library's root must be its own folder")

	roots := map[string]Folder{}
	for _, f := range tree.Folders {
		assert.Equal(t, "root:", f.Path)
		roots[f.DriveName] = f
	}

	require.Contains(t, roots, "library-one")
	require.Contains(t, roots, "library-two")
	assert.NotEqual(t, roots["library-one"].ID, roots["library-two"].ID,
		"two libraries' roots must not share an ID")

	// And each library's file must be reported inside its own root.
	for _, test := range []struct{ library, file string }{
		{library: "library-one", file: "one.txt"},
		{library: "library-two", file: "two.txt"},
	} {
		list, err := r.ListItems(ctx, ItemsRequest{
			BackupPointID: id,
			FolderID:      roots[test.library].ID,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{test.file}, itemNames(list.Items),
			"%s must contain only its own file", test.library)
	}
}

// ---------------------------------------------------------------------------
// Done when #3: items with stable IDs, names, sizes and dates.
// ---------------------------------------------------------------------------

// TestListItems_ReturnsIDNameSizeAndDates proves every item lists with
// all four of the brief's required attributes, for every seeded workload
// including the two whose name does not live in an ItemName field (an
// Exchange contact, and a SharePoint list).
func TestListItems_ReturnsIDNameSizeAndDates(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, id, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	list, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id})
	require.NoError(t, err)

	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)

	assert.Equal(t, id, list.BackupPointID)
	assert.True(t, list.IndependentlyRestorable)

	// Ordering is deterministic and documented: by folder path, then
	// name, then ID. The SharePoint list has no folder, so its empty
	// folder path sorts it first.
	assert.Equal(
		t,
		[]string{
			"k3ep-seed-event-001",    // k3ep-seed-calendar
			"k3ep-seed-contact-001",  // k3ep-seed-contacts
			"k3ep-seed-mail-001",     // k3ep-seed-mail
			"k3ep-seed-mail-002",     // k3ep-seed-mail
			"k3ep-seed-list",         // list-0000-1111 (the list's own container)
			"k3ep-seed-doc-001.txt",  // root: (the library drive)
			"k3ep-seed-file-001.txt", // root:/k3ep-seed
			"k3ep-seed-file-002.txt", // root:/k3ep-seed
		},
		itemNames(list.Items),
		"items must be ordered by folder path, then name, then ID")

	byName := map[string]Item{}

	for _, it := range list.Items {
		require.NotEmpty(t, it.ID, "every item must carry an ID")
		require.NotEmpty(t, it.Name, "every seeded item must carry a name")
		byName[it.Name] = it
	}

	require.Len(t, byName, 8, "every seeded item must be listed exactly once")

	mail := byName["k3ep-seed-mail-001"]
	assert.Equal(t, int64(1024), mail.SizeBytes)
	assert.Equal(t, mailCreated, mail.CreatedAt)
	assert.Equal(t, mailModified, mail.ModifiedAt)
	assert.Equal(t, "exchange", mail.Service)
	assert.Equal(t, "email", mail.Category)
	assert.Equal(t, "k3ep-seed-mail", mail.FolderPath)
	assert.NotEmpty(t, mail.FolderID)

	contact := byName["k3ep-seed-contact-001"]
	assert.Equal(t, "contacts", contact.Category)
	assert.Equal(t, "k3ep-seed-contacts", contact.FolderPath)

	event := byName["k3ep-seed-event-001"]
	assert.Equal(t, "events", event.Category)

	file := byName["k3ep-seed-file-001.txt"]
	assert.Equal(t, int64(34), file.SizeBytes)
	assert.Equal(t, fileCreated, file.CreatedAt)
	assert.Equal(t, fileModified, file.ModifiedAt)
	assert.Equal(t, "root:/k3ep-seed", file.FolderPath)
	assert.Equal(t, testDrive, file.DriveID)

	doc := byName["k3ep-seed-doc-001.txt"]
	assert.Equal(t, "libraries", doc.Category)
	assert.Equal(t, "k3ep-seed-library", doc.DriveName)

	// A SharePoint list's name comes from its list info, not ItemName,
	// and its container folder is named by the list's own ID.
	lst := byName["k3ep-seed-list"]
	assert.Equal(t, "lists", lst.Category)
	assert.Equal(t, int64(2), lst.ItemCount,
		"a list reports how many rows the backup captured inside it, since they are not separate items")
	assert.Equal(t, "list-0000-1111", lst.FolderPath,
		"a list's container is recorded under the list's own ID, not its title")
	assert.Equal(t, folderByPath(t, tree.Folders, "list-0000-1111").ID, lst.FolderID)

	// Every item's FolderID, where set, must name a folder ListFolders
	// actually returns.
	for _, it := range list.Items {
		if len(it.FolderID) == 0 {
			continue
		}

		assert.True(t, folderExists(tree.Folders, it.FolderID),
			"item %s names folder %s, which ListFolders does not return", it.ID, it.FolderID)
	}
}

// TestListItems_ItemWithNoRecordedLocationStillLists covers
// Item.FolderID's documented "" case. No v1 workload produces such an
// entry - every item the test tenant's own backups record sits in a
// folder, including a SharePoint list - but browse must report one
// rather than drop it, because an item it silently omitted would be
// invisible to restore and to erasure alike.
func TestListItems_ItemWithNoRecordedLocationStillLists(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addLocationlessItem(t, b, "orphan-001", "an-item-with-no-location")

	r, id, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	list, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id})
	require.NoError(t, err)

	require.Len(t, list.Items, 1, "an item with no recorded location must still be listed")
	assert.Equal(t, "an-item-with-no-location", list.Items[0].Name)
	assert.Empty(t, list.Items[0].FolderID)
	assert.Empty(t, list.Items[0].FolderPath)
	assert.NotEmpty(t, list.Items[0].ID)
}

// TestListItems_IDsAreStableAcrossSeparateCalls is the brief's own
// criterion 3 guarantee: 5.1's restore and 7.3's erasure both take these
// IDs as input, so an ID that changed between the call that showed it to
// an admin and the call that acted on it would be worse than useless.
//
// Two separate ListItems calls, against two separately-marshalled copies
// of the same manifest (so nothing is shared by pointer between them),
// must agree ID-for-ID. The folder IDs must be stable on the same terms,
// since ListItems' own FolderID filter takes one.
//
// This test fails if the IDs were generated per call: verified by
// replacing browse's `ID: ent.ShortRef` with a fresh uuid.NewString() on
// each call, which turns both assertions below red.
func TestListItems_IDsAreStableAcrossSeparateCalls(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := seededDetails(t)

	r1, id1, _ := repoWithDetails(t, b.Details(), model.MergeBackup)
	r2, id2, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	firstItems, err := r1.ListItems(ctx, ItemsRequest{BackupPointID: id1})
	require.NoError(t, err)
	require.NotEmpty(t, firstItems.Items)

	secondItems, err := r2.ListItems(ctx, ItemsRequest{BackupPointID: id2})
	require.NoError(t, err)

	firstByName := map[string]string{}
	for _, it := range firstItems.Items {
		firstByName[it.Name] = it.ID
	}

	secondByName := map[string]string{}
	for _, it := range secondItems.Items {
		secondByName[it.Name] = it.ID
	}

	assert.Equal(t, firstByName, secondByName,
		"the same item must report the same ID on a later, separate call")

	firstTree, err := r1.ListFolders(ctx, FolderTreeRequest{BackupPointID: id1})
	require.NoError(t, err)

	secondTree, err := r2.ListFolders(ctx, FolderTreeRequest{BackupPointID: id2})
	require.NoError(t, err)

	firstFolders := map[string]string{}
	for _, f := range firstTree.Folders {
		firstFolders[f.Path+"|"+f.Service+"|"+f.Category] = f.ID
	}

	secondFolders := map[string]string{}
	for _, f := range secondTree.Folders {
		secondFolders[f.Path+"|"+f.Service+"|"+f.Category] = f.ID
	}

	assert.Equal(t, firstFolders, secondFolders,
		"the same folder must report the same ID on a later, separate call")

	// And an ID listed on the first call must still select the same
	// items on the second - the actual property 5.1 and 7.3 depend on.
	mailFolderID := firstFolders["k3ep-seed-mail|exchange|email"]
	require.NotEmpty(t, mailFolderID)

	again, err := r2.ListItems(ctx, ItemsRequest{BackupPointID: id2, FolderID: mailFolderID})
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"k3ep-seed-mail-001", "k3ep-seed-mail-002"},
		itemNames(again.Items),
		"a folder ID from an earlier call must still select that folder's items")
}

// TestListItems_DriveItemIDChangesWhenAnAncestorFolderIsRenamed pins the
// limit Item.ID documents, which 5.1 and 7.3 will build on: a drive
// item's storage path is built from its folder chain's DISPLAY NAMES, so
// renaming (or moving) an ancestor gives the same file a different ID in
// the next backup point.
//
// Not a defect to fix - it is the fork's own path scheme - but the
// difference between "(point, ID) is the handle" and "ID is the handle",
// and an earlier version of Item.ID's doc comment called the path
// "immutable", which would have misled a caller into using the ID as a
// cross-point primary key. Found in review.
func TestListItems_DriveItemIDChangesWhenAnAncestorFolderIsRenamed(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	// The same file, same item ID, same drive - only the containing
	// folder's display name differs, exactly as a rename between two
	// backups would leave it.
	before := &details.Builder{}
	addDriveFile(t, before, "file-001", "report.txt", 10, false, "Quarterlies")

	after := &details.Builder{}
	addDriveFile(t, after, "file-001", "report.txt", 10, false, "Quarterly Reports")

	rBefore, idBefore, _ := repoWithDetails(t, before.Details(), model.MergeBackup)
	rAfter, idAfter, _ := repoWithDetails(t, after.Details(), model.MergeBackup)

	listBefore, err := rBefore.ListItems(ctx, ItemsRequest{BackupPointID: idBefore})
	require.NoError(t, err)
	require.Len(t, listBefore.Items, 1)

	listAfter, err := rAfter.ListItems(ctx, ItemsRequest{BackupPointID: idAfter})
	require.NoError(t, err)
	require.Len(t, listAfter.Items, 1)

	assert.Equal(t, listBefore.Items[0].Name, listAfter.Items[0].Name,
		"this test is only meaningful if it is the same file either side")
	assert.NotEqual(t, listBefore.Items[0].ID, listAfter.Items[0].ID,
		"renaming a drive folder must be understood to change its items' IDs; "+
			"if this now passes, Item.ID's documented contract is stronger than stated "+
			"and should be re-read before 5.1 relies on it")
}

// TestListItems_ExchangeItemIDSurvivesAFolderRename is the other half of
// that contract: Exchange storage paths use container IDs, not display
// names, so renaming a mail folder leaves its items' IDs alone. Without
// this case the test above would be consistent with "IDs are never
// stable across points", which is not what the fork does and not what
// Item.ID says.
func TestListItems_ExchangeItemIDSurvivesAFolderRename(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	// Same mailbox, same folder container ID, same message - only the
	// folder's display name (its recorded location) differs.
	build := func(folderDisplayName string) *details.Details {
		b := &details.Builder{}

		rr, err := path.Build(testTenant, testUser, path.ExchangeService, path.EmailCategory, true,
			"mail-folder-container-id", "msg-001")
		require.NoError(t, err)

		require.NoError(t, b.Add(
			rr,
			path.Builder{}.Append(folderDisplayName),
			details.ItemInfo{
				Exchange: &details.ExchangeInfo{
					ItemType: details.ExchangeMail,
					Subject:  "quarterly report",
					Size:     10,
					Created:  mailCreated,
					Modified: mailModified,
				},
			}))

		return b.Details()
	}

	rBefore, idBefore, _ := repoWithDetails(t, build("Quarterlies"), model.MergeBackup)
	rAfter, idAfter, _ := repoWithDetails(t, build("Quarterly Reports"), model.MergeBackup)

	listBefore, err := rBefore.ListItems(ctx, ItemsRequest{BackupPointID: idBefore})
	require.NoError(t, err)
	require.Len(t, listBefore.Items, 1)

	listAfter, err := rAfter.ListItems(ctx, ItemsRequest{BackupPointID: idAfter})
	require.NoError(t, err)
	require.Len(t, listAfter.Items, 1)

	assert.Equal(t, listBefore.Items[0].ID, listAfter.Items[0].ID,
		"renaming an Exchange mail folder must not change its items' IDs")

	// The folder's own ID is likewise unchanged, while its reported name
	// follows the rename - which is the point of keeping both.
	treeBefore, err := rBefore.ListFolders(ctx, FolderTreeRequest{BackupPointID: idBefore})
	require.NoError(t, err)

	treeAfter, err := rAfter.ListFolders(ctx, FolderTreeRequest{BackupPointID: idAfter})
	require.NoError(t, err)

	require.Len(t, treeBefore.Folders, 1)
	require.Len(t, treeAfter.Folders, 1)

	assert.Equal(t, treeBefore.Folders[0].ID, treeAfter.Folders[0].ID)
	assert.Equal(t, "Quarterlies", treeBefore.Folders[0].Name)
	assert.Equal(t, "Quarterly Reports", treeAfter.Folders[0].Name)
}

// TestListItems_FilterByFolderIDIsDirectChildrenOnly proves the
// FolderID filter returns the items directly in that folder and not its
// subfolders', as ItemsRequest.FolderID documents.
func TestListItems_FilterByFolderIDIsDirectChildrenOnly(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addDriveFile(t, b, "top-001", "top.txt", 10, false, "k3ep-seed")
	addDriveFile(t, b, "nested-001", "nested.txt", 20, false, "k3ep-seed", "deeper")

	r, id, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)

	parent := folderByPath(t, tree.Folders, "root:/k3ep-seed")
	child := folderByPath(t, tree.Folders, "root:/k3ep-seed/deeper")
	assert.Equal(t, parent.ID, child.ParentID)

	inParent, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id, FolderID: parent.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"top.txt"}, itemNames(inParent.Items),
		"the parent folder's listing must not include its subfolder's items")

	inChild, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id, FolderID: child.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"nested.txt"}, itemNames(inChild.Items))
}

// TestListItems_UnknownFolderIDIsAnError proves an unknown folder ID
// fails loudly rather than returning an empty list a caller would read
// as "this folder is empty".
func TestListItems_UnknownFolderIDIsAnError(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, id, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	_, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id, FolderID: "not-a-folder"})
	assert.ErrorIs(t, err, ErrFolderNotFound)
}

// TestListItems_ExcludesMetadataEntries proves the fork's OneDrive
// permission sidecar entries are never returned as items. They are not
// something an admin backed up, and presenting one as a restorable item
// would be wrong.
func TestListItems_ExcludesMetadataEntries(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addDriveFile(t, b, "file-001", "real.txt", 10, false, "k3ep-seed")
	addDriveFile(t, b, "file-001.meta", "real.txt.meta", 1, true, "k3ep-seed")

	r, id, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	list, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id})
	require.NoError(t, err)
	assert.Equal(t, []string{"real.txt"}, itemNames(list.Items))
}

// ---------------------------------------------------------------------------
// The assist/preview trap: not every listed backup point is browsable.
// ---------------------------------------------------------------------------

// TestBrowse_RefusesPointsThatAreNotIndependentlyRestorable is the
// deliberate decision ErrIncompleteBackupPoint documents, under test for
// every non-merge classification: an assist point (a real backup
// interrupted by a recoverable error), a preview point (deliberately
// partial), and an unclassified point (no backup-type tag and no
// version, which 0.6 deliberately classifies unknown rather than merge).
//
// Each one's folder tree is a fragment of the object with nothing inside
// the data saying so, so browse refuses it by default rather than
// rendering it next to a merge point's tree as though it were a complete
// point in time.
func TestBrowse_RefusesPointsThatAreNotIndependentlyRestorable(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	table := []struct {
		name      string
		tag       string
		expect    BackupPointType
		noVersion bool
	}{
		{name: "assist", tag: model.AssistBackup, expect: BackupPointTypeAssist},
		{name: "preview", tag: model.PreviewBackup, expect: BackupPointTypePreview},
		{name: "unclassified", noVersion: true, expect: BackupPointTypeUnknown},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			deets := seededDetails(t).Details()
			id := uuid.NewString()

			tags := map[string]string{"k3ep-object": testUser}
			if len(test.tag) > 0 {
				tags[model.BackupTypeTag] = test.tag
			}

			b := &backup.Backup{
				BaseModel:    model.BaseModel{ID: model.StableID(id), Tags: tags},
				CreationTime: time.Now().UTC(),
			}

			if !test.noVersion {
				b.Version = version.Backup
			}

			fr := &fakeRepo{
				backups: []*backup.Backup{b},
				deets:   map[string]*details.Details{id: deets},
			}
			r := &Repository{repo: fr}

			_, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
			assert.ErrorIs(t, err, ErrIncompleteBackupPoint,
				"ListFolders must refuse a %s point by default", test.name)

			_, err = r.ListItems(ctx, ItemsRequest{BackupPointID: id})
			assert.ErrorIs(t, err, ErrIncompleteBackupPoint,
				"ListItems must refuse a %s point by default", test.name)

			// The opt-in exists so a future erasure chunk can still
			// enumerate what an incomplete point holds (GDPR-2), and it
			// must flag what the caller got.
			tree, err := r.ListFolders(ctx, FolderTreeRequest{
				BackupPointID:        id,
				AllowIncompletePoint: true,
			})
			require.NoError(t, err)
			assert.Equal(t, test.expect, tree.PointType)
			assert.False(t, tree.IndependentlyRestorable,
				"an opted-in browse of an incomplete point must still report it as not restorable")
			assert.NotEmpty(t, tree.Folders, "the opt-in must actually return the fragment")

			list, err := r.ListItems(ctx, ItemsRequest{
				BackupPointID:        id,
				AllowIncompletePoint: true,
			})
			require.NoError(t, err)
			assert.Equal(t, test.expect, list.PointType)
			assert.False(t, list.IndependentlyRestorable)
			assert.NotEmpty(t, list.Items)
		})
	}
}

// TestBrowse_MergePointIsBrowsableWithoutOptIn is the control for the
// test above: without it, a bug that refused every point would look like
// a pass.
func TestBrowse_MergePointIsBrowsableWithoutOptIn(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, id, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)
	assert.True(t, tree.IndependentlyRestorable)
	assert.NotEmpty(t, tree.Folders)
}

// ---------------------------------------------------------------------------
// Done when #4: finding by name, including the negative case.
// ---------------------------------------------------------------------------

// TestFindItemsByName_MatchesAndExcludesNonMatching is the brief's
// criterion 4: the expected items come back and the non-matching ones do
// not. The negative cases are deliberate near-misses - an item in the
// same folder with a different name, and an item whose name differs only
// after the matched prefix - rather than obviously unrelated data.
func TestFindItemsByName_MatchesAndExcludesNonMatching(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addMail(t, b, "k3ep-seed-mail", "msg-001", "quarterly report", 10)
	addMail(t, b, "k3ep-seed-mail", "msg-002", "quarterly reporting pack", 20)
	addMail(t, b, "k3ep-seed-mail", "msg-003", "lunch", 30)
	addDriveFile(t, b, "file-001", "quarterly-report.xlsx", 40, false, "k3ep-seed")
	addDriveFile(t, b, "file-002", "holiday-photo.jpg", 50, false, "k3ep-seed")

	r, _, _ := repoWithDetails(t, b.Details(), model.MergeBackup)

	tags := map[string]string{"k3ep-object": testUser}

	found, err := r.FindItemsByName(ctx, FindItemsRequest{Tags: tags, Name: "quarterly report"})
	require.NoError(t, err)

	assert.Empty(t, found.UnreadableBackupPointIDs,
		"every point in this fixture is readable, so none may be reported unreadable")

	names := make([]string, 0, len(found.Items))
	for _, f := range found.Items {
		names = append(names, f.Item.Name)
	}

	assert.ElementsMatch(t, []string{"quarterly report", "quarterly reporting pack"}, names)
	assert.NotContains(t, names, "lunch", "a non-matching item in the same folder must be excluded")
	assert.NotContains(t, names, "quarterly-report.xlsx",
		"a near-miss name must be excluded: the hyphenated filename does not contain the query")
	assert.NotContains(t, names, "holiday-photo.jpg")

	// Every hit must carry enough to act on: the point it came from, and
	// a folder and ID a later restore can use.
	for _, f := range found.Items {
		assert.NotEmpty(t, f.BackupPointID)
		assert.Equal(t, BackupPointTypeMerge, f.BackupPointType)
		assert.True(t, f.BackupPointIndependentlyRestorable)
		assert.False(t, f.BackupPointCreatedAt.IsZero())
		assert.NotEmpty(t, f.Item.ID)
		assert.Equal(t, "k3ep-seed-mail", f.Item.FolderPath)
	}

	// A query matching nothing returns nothing, not everything.
	none, err := r.FindItemsByName(ctx, FindItemsRequest{Tags: tags, Name: "no-such-item"})
	require.NoError(t, err)
	assert.Empty(t, none.Items)
}

// TestFindItemsByName_ExactModeExcludesSubstringMatches proves
// NameMatchExact is genuinely stricter than the default, and that the
// default is genuinely substring.
func TestFindItemsByName_ExactModeExcludesSubstringMatches(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addMail(t, b, "k3ep-seed-mail", "msg-001", "report", 10)
	addMail(t, b, "k3ep-seed-mail", "msg-002", "report draft", 20)

	r, _, _ := repoWithDetails(t, b.Details(), model.MergeBackup)
	tags := map[string]string{"k3ep-object": testUser}

	exact, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:  tags,
		Name:  "report",
		Match: NameMatchExact,
	})
	require.NoError(t, err)
	require.Len(t, exact.Items, 1)
	assert.Equal(t, "report", exact.Items[0].Item.Name)

	contains, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:  tags,
		Name:  "report",
		Match: NameMatchContains,
	})
	require.NoError(t, err)
	assert.Len(t, contains.Items, 2, "the zero value of Match must be substring matching")
}

// TestFindItemsByName_IsCaseInsensitive covers NameMatch's documented
// case handling, in both modes.
func TestFindItemsByName_IsCaseInsensitive(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	b := &details.Builder{}
	addMail(t, b, "k3ep-seed-mail", "msg-001", "Quarterly Report", 10)

	r, _, _ := repoWithDetails(t, b.Details(), model.MergeBackup)
	tags := map[string]string{"k3ep-object": testUser}

	contains, err := r.FindItemsByName(ctx, FindItemsRequest{Tags: tags, Name: "QUARTERLY rep"})
	require.NoError(t, err)
	assert.Len(t, contains.Items, 1)

	exact, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:  tags,
		Name:  "quarterly report",
		Match: NameMatchExact,
	})
	require.NoError(t, err)
	assert.Len(t, exact.Items, 1)
}

// TestFindItemsByName_SkipsIncompletePointsUnlessAskedProves a find does
// not reach into an assist point by default, and does when asked - the
// same deliberate boundary as ListFolders, but skipping rather than
// failing, because a find spans many points.
func TestFindItemsByName_SkipsIncompletePointsUnlessAsked(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	mergeB := &details.Builder{}
	addMail(t, mergeB, "k3ep-seed-mail", "msg-001", "in the merge point", 10)

	assistB := &details.Builder{}
	addMail(t, assistB, "k3ep-seed-mail", "msg-002", "in the assist point", 20)

	mergeID, assistID := uuid.NewString(), uuid.NewString()
	tags := map[string]string{"k3ep-object": testUser}

	fr := &fakeRepo{
		backups: []*backup.Backup{
			{
				BaseModel: model.BaseModel{
					ID: model.StableID(mergeID),
					Tags: map[string]string{
						model.BackupTypeTag: model.MergeBackup,
						"k3ep-object":       testUser,
					},
				},
				CreationTime: time.Now().UTC().Add(-time.Hour),
				Version:      version.Backup,
			},
			{
				BaseModel: model.BaseModel{
					ID: model.StableID(assistID),
					Tags: map[string]string{
						model.BackupTypeTag: model.AssistBackup,
						"k3ep-object":       testUser,
					},
				},
				CreationTime: time.Now().UTC(),
				Version:      version.Backup,
			},
		},
		deets: map[string]*details.Details{
			mergeID:  mergeB.Details(),
			assistID: assistB.Details(),
		},
	}
	r := &Repository{repo: fr}

	byDefault, err := r.FindItemsByName(ctx, FindItemsRequest{Tags: tags, Name: "in the"})
	require.NoError(t, err)
	require.Len(t, byDefault.Items, 1)
	assert.Equal(t, "in the merge point", byDefault.Items[0].Item.Name)
	assert.Equal(t, mergeID, byDefault.Items[0].BackupPointID)
	assert.Empty(t, byDefault.UnreadableBackupPointIDs,
		"a point skipped for being incomplete is not an unreadable point")
	assert.Equal(t, 1, fr.detailsCalls,
		"a skipped point's details manifest must not even be read")

	optedIn, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:                  tags,
		Name:                  "in the",
		AllowIncompletePoints: true,
	})
	require.NoError(t, err)
	require.Len(t, optedIn.Items, 2)

	// Newest point first, as FindItemsByName documents.
	assert.Equal(t, assistID, optedIn.Items[0].BackupPointID)
	assert.Equal(t, BackupPointTypeAssist, optedIn.Items[0].BackupPointType)
	assert.False(t, optedIn.Items[0].BackupPointIndependentlyRestorable)
	assert.Equal(t, mergeID, optedIn.Items[1].BackupPointID)
	assert.True(t, optedIn.Items[1].BackupPointIndependentlyRestorable)
}

// TestFindItemsByName_UnreadablePointIsSkippedAndReported proves one bad
// backup point does not make an object unsearchable.
//
// Found in review: FindItemsByName used to return the read error, so a
// single point whose details could not be read - including the ordinary,
// benign race where retention or a concurrent DeleteBackupPoints removes
// a point between this call's own listing step and its read - failed the
// whole search. An object with any one damaged point would have been
// completely unsearchable, which for an erasure-verification or
// legal-hold query is the worst possible failure mode: it looks like a
// system error rather than a partial answer.
//
// Both halves matter and both are asserted: the matches from the
// readable points still come back, AND the skipped point is named, so a
// caller can tell "nothing matched" from "not everything was searched".
func TestFindItemsByName_UnreadablePointIsSkippedAndReported(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	goodB := &details.Builder{}
	addMail(t, goodB, "k3ep-seed-mail", "msg-001", "quarterly report", 10)

	otherGoodB := &details.Builder{}
	addMail(t, otherGoodB, "k3ep-seed-mail", "msg-002", "quarterly report v2", 20)

	badB := &details.Builder{}
	addMail(t, badB, "k3ep-seed-mail", "msg-003", "quarterly report v3", 30)

	goodID, badID, otherGoodID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tags := map[string]string{"k3ep-object": testUser}

	mergePoint := func(id string, age time.Duration) *backup.Backup {
		return &backup.Backup{
			BaseModel: model.BaseModel{
				ID: model.StableID(id),
				Tags: map[string]string{
					model.BackupTypeTag: model.MergeBackup,
					"k3ep-object":       testUser,
				},
			},
			CreationTime: time.Now().UTC().Add(-age),
			Version:      version.Backup,
		}
	}

	// The unreadable point sits in the MIDDLE by age, so a search that
	// aborted on it would also lose the oldest point's match - proving
	// the loop continues rather than merely tolerating a failure at the
	// end.
	fr := &fakeRepo{
		backups: []*backup.Backup{
			mergePoint(otherGoodID, 0),
			mergePoint(badID, time.Hour),
			mergePoint(goodID, 2*time.Hour),
		},
		deets: map[string]*details.Details{
			otherGoodID: otherGoodB.Details(),
			badID:       badB.Details(),
			goodID:      goodB.Details(),
		},
		detailsFailureByID: map[string]error{badID: assert.AnError},
	}
	r := &Repository{repo: fr}

	found, err := r.FindItemsByName(ctx, FindItemsRequest{Tags: tags, Name: "quarterly report"})
	require.NoError(t, err, "one unreadable point must not fail the whole search")

	names := make([]string, 0, len(found.Items))
	for _, f := range found.Items {
		names = append(names, f.Item.Name)
	}

	assert.ElementsMatch(t, []string{"quarterly report v2", "quarterly report"}, names,
		"matches from every readable point must still be returned")
	assert.NotContains(t, names, "quarterly report v3",
		"the unreadable point's items must not appear")

	assert.Equal(t, []string{badID}, found.UnreadableBackupPointIDs,
		"the skipped point must be named, or a partial result is indistinguishable from a complete one")

	// A point missing entirely - deleted between the listing and the read
	// - is the same case, and must behave the same way rather than
	// surfacing as ErrBackupPointNotFound.
	frDeleted := &fakeRepo{
		backups: []*backup.Backup{mergePoint(goodID, 0), mergePoint(badID, time.Hour)},
		deets:   map[string]*details.Details{goodID: goodB.Details()},
	}
	rDeleted := &Repository{repo: frDeleted}

	afterDelete, err := rDeleted.FindItemsByName(ctx, FindItemsRequest{
		Tags: tags,
		Name: "quarterly report",
	})
	require.NoError(t, err, "a point deleted mid-search must not fail the search")
	require.Len(t, afterDelete.Items, 1)
	assert.Equal(t, "quarterly report", afterDelete.Items[0].Item.Name)
	assert.Equal(t, []string{badID}, afterDelete.UnreadableBackupPointIDs)
}

// ---------------------------------------------------------------------------
// Guard rails.
// ---------------------------------------------------------------------------

func TestFindItemsByName_RequiresTagsAndName(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, _, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	_, err := r.FindItemsByName(ctx, FindItemsRequest{Name: "anything"})
	assert.ErrorIs(t, err, ErrEmptyTagFilter,
		"an unscoped find would search every tenant in the repository")

	_, err = r.FindItemsByName(ctx, FindItemsRequest{
		Tags: map[string]string{"k3ep-object": testUser},
	})
	assert.ErrorIs(t, err, ErrEmptyNameQuery,
		"an empty query must not be read as 'every item'")
}

func TestBrowse_RequiresABackupPointID(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, _, fr := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	_, err := r.ListFolders(ctx, FolderTreeRequest{})
	assert.ErrorIs(t, err, ErrMissingBackupPointID)

	_, err = r.ListItems(ctx, ItemsRequest{})
	assert.ErrorIs(t, err, ErrMissingBackupPointID)

	assert.Equal(t, 0, fr.detailsCalls,
		"an empty backup point ID must be rejected before any read")
}

func TestBrowse_UnknownBackupPointIsNotFound(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	r, _, _ := repoWithDetails(t, seededDetails(t).Details(), model.MergeBackup)

	_, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: uuid.NewString()})
	assert.ErrorIs(t, err, ErrBackupPointNotFound)
	// The exported sentinel must also match the fork's own error, so a
	// caller inside this module can keep using either.
	assert.ErrorIs(t, err, repository.ErrorBackupNotFound)

	_, err = r.ListItems(ctx, ItemsRequest{BackupPointID: uuid.NewString()})
	assert.ErrorIs(t, err, ErrBackupPointNotFound)
}

// TestBrowse_ErrorsNeverNameAnItem protects the fork's convention that
// errors name items by ID, not by subject or filename (CLAUDE.md), which
// browse is the one chunk with a real incentive to break: its whole job
// is names.
//
// browseEntries has exactly four error paths that carry entry context -
// a folder's location, a folder's storage path, an item's location and
// an item's storage path - and it returns on the first one, so a single
// fixture can only ever reach one of them. An earlier version of this
// test used one malformed *item* entry with a valid location and claimed
// in its own comment that "every error path that carries entry context
// fires", which was false: three of the four branches were unreachable,
// and a name interpolated into any of them left this test green. Found
// in review. Hence one case per branch below, each reaching its branch
// and no other.
//
// Each case asserts the message carries the entry's opaque ID and none
// of the three places a name could come from: the folder display name,
// the location (which is itself a path of display names) and the item
// name. The mutation matrix proving each case genuinely fires is
// recorded in this chunk's result; every one of the eight mutations run
// reddened exactly the case for its branch.
func TestBrowse_ErrorsNeverNameAnItem(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	// A location that cannot be unescaped: a backslash followed by a
	// character that is not a valid escape. The fork's own error for it
	// ("bad escape sequence in path") does not quote the input, so a leak
	// here could only come from browse's own formatting - which is
	// exactly what is under test.
	const (
		badLocation   = `secret-parent-folder\qsecret`
		goodLocation  = "secret-location-folder"
		secretFolder  = "a-very-secret-folder-name"
		secretSubject = "a-very-secret-subject-line"
		badRepoRef    = "not/a/data/layer/path/at-all/really"
		shortRef      = "abcdef123456"
	)

	goodFolderRepoRef, err := path.Build(
		testTenant, testUser, path.ExchangeService, path.EmailCategory, false, "folder-id")
	require.NoError(t, err)

	goodItemRepoRef, err := path.Build(
		testTenant, testUser, path.ExchangeService, path.EmailCategory, true, "folder-id", "item-id")
	require.NoError(t, err)

	folderInfo := func() details.ItemInfo {
		return details.ItemInfo{
			Folder: &details.FolderInfo{
				ItemType:    details.FolderItem,
				DisplayName: secretFolder,
			},
		}
	}

	mailInfo := func() details.ItemInfo {
		return details.ItemInfo{
			Exchange: &details.ExchangeInfo{
				ItemType: details.ExchangeMail,
				Subject:  secretSubject,
			},
		}
	}

	table := []struct {
		name  string
		entry details.Entry
	}{
		{
			// Branch 1: a folder entry whose location cannot be parsed.
			name: "folder location",
			entry: details.Entry{
				RepoRef:     goodFolderRepoRef.String(),
				ShortRef:    shortRef,
				LocationRef: badLocation,
				ItemInfo:    folderInfo(),
			},
		},
		{
			// Branch 2: a folder entry whose storage path cannot be
			// parsed. Its location must be valid, or branch 1 fires
			// first.
			name: "folder path",
			entry: details.Entry{
				RepoRef:     badRepoRef,
				ShortRef:    shortRef,
				LocationRef: goodLocation,
				ItemInfo:    folderInfo(),
			},
		},
		{
			// Branch 3: an item entry whose location cannot be parsed.
			name: "item location",
			entry: details.Entry{
				RepoRef:     goodItemRepoRef.String(),
				ShortRef:    shortRef,
				LocationRef: badLocation,
				ItemInfo:    mailInfo(),
			},
		},
		{
			// Branch 4: an item entry whose storage path cannot be
			// parsed - the only branch the earlier fixture reached.
			name: "item path",
			entry: details.Entry{
				RepoRef:     badRepoRef,
				ShortRef:    shortRef,
				LocationRef: goodLocation,
				ItemInfo:    mailInfo(),
			},
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			deets := &details.Details{
				DetailsModel: details.DetailsModel{Entries: []details.Entry{test.entry}},
			}

			r, id, _ := repoWithDetails(t, deets, model.MergeBackup)

			// Both entry points must be covered: ListFolders and
			// ListItems share browseEntries, so whichever branch this
			// case reaches, both calls must reach it.
			_, folderErr := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
			_, itemErr := r.ListItems(ctx, ItemsRequest{BackupPointID: id})

			for _, err := range []error{folderErr, itemErr} {
				require.Error(t, err, "this case must actually reach an error branch")

				msg := err.Error()

				assert.Contains(t, msg, shortRef,
					"an entry error must identify the entry by its opaque ID")
				assert.NotContains(t, msg, secretFolder,
					"an error must never carry a folder's display name")
				assert.NotContains(t, msg, secretSubject,
					"an error must never carry an item's subject")
				assert.NotContains(t, msg, goodLocation,
					"an error must never carry a location, which is a path of display names")
				assert.NotContains(t, msg, badLocation,
					"an error must never carry a location, even an unparseable one")
			}
		})
	}
}

// TestBrowse_ReadErrorIsReportedNotSwallowed proves a details read that
// fails for any reason other than not-found surfaces as an error rather
// than as an empty tree, which a caller would read as "this backup point
// contains nothing".
func TestBrowse_ReadErrorIsReportedNotSwallowed(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	id := uuid.NewString()
	fr := &fakeRepo{
		backups: []*backup.Backup{{
			BaseModel: model.BaseModel{
				ID:   model.StableID(id),
				Tags: map[string]string{model.BackupTypeTag: model.MergeBackup},
			},
			Version: version.Backup,
		}},
		detailsFailure: assert.AnError,
	}
	r := &Repository{repo: fr}

	_, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, ErrBackupPointNotFound,
		"a read failure is not the same as a missing backup point")
}

// TestNameMatches covers the matcher's own edge cases directly, including
// the one a substring matcher gets wrong by default: an item with no
// recorded name must never match a real query, even though the empty
// string is a substring of everything.
func TestNameMatches(t *testing.T) {
	table := []struct {
		name   string
		item   string
		query  string
		mode   NameMatch
		expect bool
	}{
		{name: "contains", item: "quarterly report", query: "report", expect: true},
		{name: "contains miss", item: "quarterly report", query: "invoice", expect: false},
		{name: "contains case", item: "Quarterly Report", query: "rEpOrT", expect: true},
		{name: "exact", item: "report", query: "report", mode: NameMatchExact, expect: true},
		{name: "exact rejects substring", item: "report draft", query: "report", mode: NameMatchExact},
		{name: "exact case", item: "Report", query: "report", mode: NameMatchExact, expect: true},
		{name: "unnamed item never matches", item: "", query: "report"},
		{name: "unnamed item never matches exactly", item: "", query: "report", mode: NameMatchExact},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expect, nameMatches(test.item, test.query, test.mode))
		})
	}
}
