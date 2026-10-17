package seed

import (
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/internal/tester"
)

// This file tests the idempotency-decision map builders for every
// workload except sharepoint.go's (which already has sharepoint_test.go,
// and was the one that was actually broken). Each of these functions is
// the pure part of its listXBySubject/listByDisplayName/etc. sibling - the
// part that decides, from an already-fetched slice, what the seeder
// already has - split out specifically so a regression here is caught by
// `go test`, not only by the gated integration test that `make check`
// skips.
type IdempotencyMapsUnitSuite struct {
	tester.Suite
}

func TestIdempotencyMapsUnitSuite(t *testing.T) {
	suite.Run(t, &IdempotencyMapsUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *IdempotencyMapsUnitSuite) TestMessagesBySubject() {
	t := suite.T()

	m1 := models.NewMessage()
	m1.SetId(ptr.To("id-1"))
	m1.SetSubject(ptr.To("k3ep-seed-mail-001"))

	m2 := models.NewMessage()
	m2.SetId(ptr.To("id-2"))
	m2.SetSubject(ptr.To("k3ep-seed-mail-002"))

	out := messagesBySubject([]models.Messageable{m1, m2})

	require.Len(t, out, 2)
	assert.Equal(t, "id-1", ptr.Val(out["k3ep-seed-mail-001"].GetId()))
	assert.Equal(t, "id-2", ptr.Val(out["k3ep-seed-mail-002"].GetId()))
	_, found := out["nonexistent-subject"]
	assert.False(t, found)
}

func (suite *IdempotencyMapsUnitSuite) TestMessagesBySubject_Empty() {
	t := suite.T()

	out := messagesBySubject(nil)
	assert.Empty(t, out)
}

func (suite *IdempotencyMapsUnitSuite) TestEventsBySubject() {
	t := suite.T()

	ev := models.NewEvent()
	ev.SetId(ptr.To("evt-1"))
	ev.SetSubject(ptr.To("k3ep-seed-event-001"))

	out := eventsBySubject([]models.Eventable{ev})

	require.Len(t, out, 1)
	assert.Equal(t, "evt-1", ptr.Val(out["k3ep-seed-event-001"].GetId()))
}

func (suite *IdempotencyMapsUnitSuite) TestEventsBySubject_Empty() {
	t := suite.T()

	out := eventsBySubject(nil)
	assert.Empty(t, out)
}

func (suite *IdempotencyMapsUnitSuite) TestContactsByDisplayName() {
	t := suite.T()

	c := models.NewContact()
	c.SetId(ptr.To("contact-1"))
	c.SetDisplayName(ptr.To("k3ep-seed-contact-001"))

	out := contactsByDisplayName([]models.Contactable{c})

	require.Len(t, out, 1)
	assert.Equal(t, "contact-1", ptr.Val(out["k3ep-seed-contact-001"].GetId()))
}

func (suite *IdempotencyMapsUnitSuite) TestContactsByDisplayName_Empty() {
	t := suite.T()

	out := contactsByDisplayName(nil)
	assert.Empty(t, out)
}

func (suite *IdempotencyMapsUnitSuite) TestDriveItemsByName() {
	t := suite.T()

	f1 := models.NewDriveItem()
	f1.SetId(ptr.To("file-1"))
	f1.SetName(ptr.To("k3ep-seed-file-001.txt"))

	f2 := models.NewDriveItem()
	f2.SetId(ptr.To("file-2"))
	f2.SetName(ptr.To("k3ep-seed-file-002.txt"))

	out := driveItemsByName([]models.DriveItemable{f1, f2})

	require.Len(t, out, 2)
	assert.Equal(t, "file-1", ptr.Val(out["k3ep-seed-file-001.txt"].GetId()))
	assert.Equal(t, "file-2", ptr.Val(out["k3ep-seed-file-002.txt"].GetId()))
}

func (suite *IdempotencyMapsUnitSuite) TestDriveItemsByName_Empty() {
	t := suite.T()

	out := driveItemsByName(nil)
	assert.Empty(t, out)
}

func (suite *IdempotencyMapsUnitSuite) TestListMatchingDisplayName() {
	t := suite.T()

	l1 := models.NewList()
	l1.SetId(ptr.To("list-1"))
	l1.SetDisplayName(ptr.To("k3ep-seed-list"))

	l2 := models.NewList()
	l2.SetId(ptr.To("list-2"))
	l2.SetDisplayName(ptr.To("some other list"))

	got := listMatchingDisplayName([]models.Listable{l1, l2}, "k3ep-seed-list")
	require.NotNil(t, got)
	assert.Equal(t, "list-1", ptr.Val(got.GetId()))

	assert.Nil(t, listMatchingDisplayName([]models.Listable{l1, l2}, "no such list"))
	assert.Nil(t, listMatchingDisplayName(nil, "k3ep-seed-list"))
}
