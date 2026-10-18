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

// addList appends one SharePoint list to b. A list is recorded as a
// single entry with no folder entries at all, and its title lives under
// List rather than in ItemName.
func addList(t *testing.T, b *details.Builder, listID, name string, itemCount int64) {
	t.Helper()

	rr, err := path.Build(testTenant, testSite, path.SharePointService, path.ListsCategory, true,
		listID)
	require.NoError(t, err)

	require.NoError(t, b.Add(
		rr,
		&path.Builder{},
		details.ItemInfo{
			SharePoint: &details.SharePointInfo{
				ItemType: details.SharePointList,
				List:     &details.ListInfo{Name: name, ItemCount: itemCount},
				Created:  fileCreated,
				Modified: fileModified,
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
	addList(t, b, "list-001", "k3ep-seed-list", 2)

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

	assert.Equal(t, id, list.BackupPointID)
	assert.True(t, list.IndependentlyRestorable)

	// Ordering is deterministic and documented: by folder path, then
	// name, then ID. The SharePoint list has no folder, so its empty
	// folder path sorts it first.
	assert.Equal(
		t,
		[]string{
			"k3ep-seed-list",            // no folder: empty folder path sorts first
			"k3ep-seed-event-001",       // k3ep-seed-calendar
			"k3ep-seed-contact-001",     // k3ep-seed-contacts
			"k3ep-seed-mail-001",        // k3ep-seed-mail
			"k3ep-seed-mail-002",        // k3ep-seed-mail
			"k3ep-seed-doc-001.txt",     // root: (the library drive)
			"k3ep-seed-file-001.txt",    // root:/k3ep-seed
			"k3ep-seed-file-002.txt",    // root:/k3ep-seed
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

	// A SharePoint list has no folder entries, so it lists with no
	// folder - and its name comes from its list info, not ItemName.
	lst := byName["k3ep-seed-list"]
	assert.Equal(t, "lists", lst.Category)
	assert.Empty(t, lst.FolderID, "a SharePoint list sits outside any folder")
	assert.Empty(t, lst.FolderPath)

	// Every item's FolderID, where set, must name a folder ListFolders
	// actually returns.
	tree, err := r.ListFolders(ctx, FolderTreeRequest{BackupPointID: id})
	require.NoError(t, err)

	for _, it := range list.Items {
		if len(it.FolderID) == 0 {
			continue
		}

		assert.True(t, folderExists(tree.Folders, it.FolderID),
			"item %s names folder %s, which ListFolders does not return", it.ID, it.FolderID)
	}
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

	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, f.Item.Name)
	}

	assert.ElementsMatch(t, []string{"quarterly report", "quarterly reporting pack"}, names)
	assert.NotContains(t, names, "lunch", "a non-matching item in the same folder must be excluded")
	assert.NotContains(t, names, "quarterly-report.xlsx",
		"a near-miss name must be excluded: the hyphenated filename does not contain the query")
	assert.NotContains(t, names, "holiday-photo.jpg")

	// Every hit must carry enough to act on: the point it came from, and
	// a folder and ID a later restore can use.
	for _, f := range found {
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
	assert.Empty(t, none)
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
	require.Len(t, exact, 1)
	assert.Equal(t, "report", exact[0].Item.Name)

	contains, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:  tags,
		Name:  "report",
		Match: NameMatchContains,
	})
	require.NoError(t, err)
	assert.Len(t, contains, 2, "the zero value of Match must be substring matching")
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
	assert.Len(t, contains, 1)

	exact, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:  tags,
		Name:  "quarterly report",
		Match: NameMatchExact,
	})
	require.NoError(t, err)
	assert.Len(t, exact, 1)
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
	require.Len(t, byDefault, 1)
	assert.Equal(t, "in the merge point", byDefault[0].Item.Name)
	assert.Equal(t, mergeID, byDefault[0].BackupPointID)
	assert.Equal(t, 1, fr.detailsCalls,
		"a skipped point's details manifest must not even be read")

	optedIn, err := r.FindItemsByName(ctx, FindItemsRequest{
		Tags:                  tags,
		Name:                  "in the",
		AllowIncompletePoints: true,
	})
	require.NoError(t, err)
	require.Len(t, optedIn, 2)

	// Newest point first, as FindItemsByName documents.
	assert.Equal(t, assistID, optedIn[0].BackupPointID)
	assert.Equal(t, BackupPointTypeAssist, optedIn[0].BackupPointType)
	assert.False(t, optedIn[0].BackupPointIndependentlyRestorable)
	assert.Equal(t, mergeID, optedIn[1].BackupPointID)
	assert.True(t, optedIn[1].BackupPointIndependentlyRestorable)
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
// The malformed entry below has a storage path browse cannot parse, so
// every error path that carries entry context fires. The assertion is
// that the resulting message carries the entry's opaque ID and none of
// the names in the manifest. This test fails if any of browse's entry
// errors is changed to interpolate a name - verified by adding the
// display name to browseEntries' folder error.
func TestBrowse_ErrorsNeverNameAnItem(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	// A details manifest holding an entry whose RepoRef is not a valid
	// data-layer path. Built by hand because details.Builder would
	// rightly refuse to create it.
	deets := &details.Details{
		DetailsModel: details.DetailsModel{
			Entries: []details.Entry{
				{
					RepoRef:     "not/a/data/layer/path/at-all/really",
					ShortRef:    "abcdef123456",
					LocationRef: "k3ep-seed-mail",
					ItemInfo: details.ItemInfo{
						Exchange: &details.ExchangeInfo{
							ItemType: details.ExchangeMail,
							Subject:  "a-very-secret-subject-line",
						},
					},
				},
			},
		},
	}

	r, id, _ := repoWithDetails(t, deets, model.MergeBackup)

	_, err := r.ListItems(ctx, ItemsRequest{BackupPointID: id})
	require.Error(t, err)

	assert.Contains(t, err.Error(), "abcdef123456",
		"an entry error must identify the entry by its opaque ID")
	assert.NotContains(t, err.Error(), "a-very-secret-subject-line",
		"an error must never carry an item's subject")
	assert.NotContains(t, err.Error(), "k3ep-seed-mail",
		"an error must never carry a folder name")
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
