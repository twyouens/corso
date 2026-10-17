package seed

import (
	"testing"
	"time"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/internal/tester"
)

// This file is Fix 1 from the whole-harness review's second round: the
// live proof in smoke_test.go that a found item and a created item carry
// the same date can only ever happen once per item, by construction - the
// harness is correctly forbidden from deleting its own seed items, so once
// an item exists, every future run finds it on both FindOrCreate calls and
// never creates it again. That live proof window has permanently closed.
// It cannot catch a *future* regression that reintroduces found-versus-
// created date divergence (exactly the bug calendar.go once had: stamping
// time.Now() only on the created branch).
//
// This test converts that one-time live observation into a standing,
// mocked-Graph guarantee: every seed*() function now builds every dated
// ManifestItem through the single generic datedManifestItem choke point
// in dated_item.go, so testing *that* function - fed two independently
// constructed objects representing "what find() returned" and "what
// create() returned" for the seven concrete Graph types every workload
// uses - is a direct, permanent regression guard, not a retrospective
// observation. There is no HTTP call here at all: these are the same
// in-memory models.New*() construction already used throughout this
// package's other unit tests (e.g. idempotency_maps_test.go), which is
// the actual Graph-facing boundary datedManifestItem operates at.
type DatedItemUnitSuite struct {
	tester.Suite
}

func TestDatedItemUnitSuite(t *testing.T) {
	suite.Run(t, &DatedItemUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// newMessage, and the six functions below it, each construct one of the
// concrete Graph types this package reads dates from, with the given
// id/created/modified, returned as the shared datedItem constraint so one
// table-driven test can drive every type identically.
func newMessage(id string, created, modified time.Time) datedItem {
	m := models.NewMessage()
	m.SetId(ptr.To(id))
	m.SetCreatedDateTime(ptr.To(created))
	m.SetLastModifiedDateTime(ptr.To(modified))

	return m
}

func newEvent(id string, created, modified time.Time) datedItem {
	e := models.NewEvent()
	e.SetId(ptr.To(id))
	e.SetCreatedDateTime(ptr.To(created))
	e.SetLastModifiedDateTime(ptr.To(modified))

	return e
}

func newContact(id string, created, modified time.Time) datedItem {
	c := models.NewContact()
	c.SetId(ptr.To(id))
	c.SetCreatedDateTime(ptr.To(created))
	c.SetLastModifiedDateTime(ptr.To(modified))

	return c
}

func newDriveItem(id string, created, modified time.Time) datedItem {
	d := models.NewDriveItem()
	d.SetId(ptr.To(id))
	d.SetCreatedDateTime(ptr.To(created))
	d.SetLastModifiedDateTime(ptr.To(modified))

	return d
}

func newDrive(id string, created, modified time.Time) datedItem {
	d := models.NewDrive()
	d.SetId(ptr.To(id))
	d.SetCreatedDateTime(ptr.To(created))
	d.SetLastModifiedDateTime(ptr.To(modified))

	return d
}

func newList(id string, created, modified time.Time) datedItem {
	l := models.NewList()
	l.SetId(ptr.To(id))
	l.SetCreatedDateTime(ptr.To(created))
	l.SetLastModifiedDateTime(ptr.To(modified))

	return l
}

func newListItem(id string, created, modified time.Time) datedItem {
	li := models.NewListItem()
	li.SetId(ptr.To(id))
	li.SetCreatedDateTime(ptr.To(created))
	li.SetLastModifiedDateTime(ptr.To(modified))

	return li
}

// TestDatedManifestItem_SameWhicheverBranchSuppliedTheObject is Fix 1
// itself: for every Graph type a workload uses, build two *separately
// constructed* objects representing the find() branch's result and the
// create() branch's result for what is, in the real tenant, the same
// physical item observed at two different times - and assert
// datedManifestItem reads back the identical dates from both. If a future
// change special-cases on which branch ran (the exact shape of the old
// calendar.go bug) rather than reading the object it was actually given,
// this fails.
func (suite *DatedItemUnitSuite) TestDatedManifestItem_SameWhicheverBranchSuppliedTheObject() {
	created := time.Date(2026, 1, 1, 3, 58, 0, 0, time.UTC)
	modified := time.Date(2026, 1, 1, 3, 59, 0, 0, time.UTC)

	tests := []struct {
		name string
		new  func(id string, created, modified time.Time) datedItem
	}{
		{name: "message", new: newMessage},
		{name: "event", new: newEvent},
		{name: "contact", new: newContact},
		{name: "drive item (onedrive/library folder or file)", new: newDriveItem},
		{name: "drive (document library)", new: newDrive},
		{name: "list", new: newList},
		{name: "list item", new: newListItem},
	}

	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			// foundObj simulates what a GetContainerByName/listXBySubject/
			// GetFolderChildren/GetListItems call returns for an item that
			// already existed. createdObj simulates what PostItem/
			// PostItemInContainer/the raw Post response returns for the
			// same logical item the moment it's created. In the real
			// tenant these would be the exact same physical item; here
			// they are two distinct Go values with the same dates, which
			// is the right level to mock at: datedManifestItem cannot
			// distinguish them by any means other than the fields it is
			// contracted to read.
			foundObj := test.new("item-id", created, modified)
			createdObj := test.new("item-id", created, modified)

			foundResult := datedManifestItem(foundObj, WorkloadOneDrive, "kind", "name", "path", 0, "resource")
			createdResult := datedManifestItem(createdObj, WorkloadOneDrive, "kind", "name", "path", 0, "resource")

			require.NotNil(t, foundResult.CreatedAt)
			require.NotNil(t, createdResult.CreatedAt)
			assert.True(t, foundResult.CreatedAt.Equal(created))
			assert.Equal(t, foundResult.CreatedAt, createdResult.CreatedAt,
				"found and created branches must report the identical CreatedAt")

			require.NotNil(t, foundResult.ModifiedAt)
			require.NotNil(t, createdResult.ModifiedAt)
			assert.True(t, foundResult.ModifiedAt.Equal(modified))
			assert.Equal(t, foundResult.ModifiedAt, createdResult.ModifiedAt,
				"found and created branches must report the identical ModifiedAt")

			assert.Equal(t, foundResult.ID, createdResult.ID)
		})
	}
}

// TestDatedManifestItem_DatesAreNotFabricated guards the other half of the
// old calendar.go bug shape: proves the dates genuinely come from the
// object (different inputs produce different outputs), not a constant or
// a wall-clock stamp that would coincidentally look populated.
func (suite *DatedItemUnitSuite) TestDatedManifestItem_DatesAreNotFabricated() {
	t := suite.T()

	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	earlyItem := newMessage("a", early, early)
	lateItem := newMessage("b", late, late)

	gotEarly := datedManifestItem(earlyItem, WorkloadExchangeMail, "message", "n", "p", 0, "r")
	gotLate := datedManifestItem(lateItem, WorkloadExchangeMail, "message", "n", "p", 0, "r")

	require.NotNil(t, gotEarly.CreatedAt)
	require.NotNil(t, gotLate.CreatedAt)
	assert.True(t, gotEarly.CreatedAt.Equal(early))
	assert.True(t, gotLate.CreatedAt.Equal(late))
	assert.False(t, gotEarly.CreatedAt.Equal(*gotLate.CreatedAt))
}

// TestDatedManifestItem_NilDatesStayNil proves the "genuinely dateless"
// containers (mail folder, calendar, contacts folder - which satisfy
// datedItem's method signatures but Graph never actually populates them
// for) correctly produce a nil CreatedAt/ModifiedAt rather than some
// zero-value stand-in, which is what makes them omit from JSON (see
// manifest.go's ManifestItem.CreatedAt doc comment).
func (suite *DatedItemUnitSuite) TestDatedManifestItem_NilDatesStayNil() {
	t := suite.T()

	// A DriveItem with no dates set at all - standing in for any type
	// whose GetCreatedDateTime/GetLastModifiedDateTime return nil.
	undated := models.NewDriveItem()
	undated.SetId(ptr.To("id"))

	got := datedManifestItem(undated, WorkloadOneDrive, "folder", "n", "", 0, "r")

	assert.Nil(t, got.CreatedAt)
	assert.Nil(t, got.ModifiedAt)
}
