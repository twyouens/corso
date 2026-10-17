package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/internal/tester"
)

type SharePointUnitSuite struct {
	tester.Suite
}

func TestSharePointUnitSuite(t *testing.T) {
	suite.Run(t, &SharePointUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// TestStringAdditionalDataValue is the regression test for a real bug found
// by running the seeder live against the test tenant: Kiota deserialises a
// SharePoint list item's Fields.AdditionalData["Title"] as a *string, not a
// string, so a plain `v.(string)` type assertion always failed, the
// "already seeded" lookup in listItemIDsByTitle always came back empty, and
// every seeder run created a fresh duplicate list item. See
// docs/test-tenant-harness.md's "Live verification findings" section for
// the full account, including the duplicate items this left in the real
// tenant.
func (suite *SharePointUnitSuite) TestStringAdditionalDataValue() {
	tests := []struct {
		name      string
		value     any
		wantOK    bool
		wantValue string
	}{
		{
			name:      "pointer to string, the real shape Kiota returns",
			value:     ptr.To("k3ep-seed-list-item-001"),
			wantOK:    true,
			wantValue: "k3ep-seed-list-item-001",
		},
		{
			name:      "bare string, in case a future SDK version changes shape",
			value:     "k3ep-seed-list-item-002",
			wantOK:    true,
			wantValue: "k3ep-seed-list-item-002",
		},
		{name: "nil pointer", value: (*string)(nil), wantOK: false},
		{name: "nil value", value: nil, wantOK: false},
		{name: "unrelated type", value: 42, wantOK: false},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			got, ok := stringAdditionalDataValue(test.value)
			assert.Equal(t, test.wantOK, ok)

			if test.wantOK {
				assert.Equal(t, test.wantValue, got)
			}
		})
	}
}
