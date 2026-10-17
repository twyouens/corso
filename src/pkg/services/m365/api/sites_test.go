package api

import (
	"strings"
	"testing"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/internal/tester/tconfig"
	"github.com/twyouens/corso/src/pkg/errs/core"
	"github.com/twyouens/corso/src/pkg/fault"
)

type SitesUnitSuite struct {
	tester.Suite
}

func TestSitesUnitSuite(t *testing.T) {
	suite.Run(t, &SitesUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *SitesUnitSuite) TestValidateSite() {
	site := models.NewSite()
	site.SetWebUrl(ptr.To("sharepoint.com/sites/foo"))
	site.SetDisplayName(ptr.To("testsite"))
	site.SetId(ptr.To("testID"))

	tests := []struct {
		name     string
		args     models.Siteable
		errCheck assert.ErrorAssertionFunc
	}{
		{
			name:     "No ID",
			args:     models.NewSite(),
			errCheck: assert.Error,
		},
		{
			name: "No WebURL",
			args: func() *models.Site {
				s := models.NewSite()
				s.SetId(ptr.To("id"))
				return s
			}(),
			errCheck: assert.Error,
		},
		{
			name: "No name",
			args: func() *models.Site {
				s := models.NewSite()
				s.SetId(ptr.To("id"))
				s.SetWebUrl(ptr.To("sharepoint.com/sites/foo"))
				return s
			}(),
			errCheck: assert.NoError,
		},
		{
			name: "Search site",
			args: func() *models.Site {
				s := models.NewSite()
				s.SetId(ptr.To("id"))
				s.SetWebUrl(ptr.To("sharepoint.com/search"))
				return s
			}(),
			errCheck: assert.NoError,
		},
		{
			name: "Personal OneDrive",
			args: func() *models.Site {
				s := models.NewSite()
				s.SetId(ptr.To("id"))
				s.SetWebUrl(ptr.To("https://" + personalSitePath + "/someone's/onedrive"))
				return s
			}(),
			errCheck: assert.NoError,
		},
		{
			name:     "Valid Site",
			args:     site,
			errCheck: assert.NoError,
		},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			err := validateSite(test.args)
			test.errCheck(t, err, clues.ToCore(err))
		})
	}
}

func (suite *SitesUnitSuite) TestIsSiteID() {
	var (
		siteCollectionID = "deadbeef-0000-0000-0000-000000000000"
		webID            = "beefdead-0000-0000-0000-000000000000"
		hostname         = "10rqc2.sharepoint.com"
	)

	tests := []struct {
		name       string
		identifier string
		expect     bool
	}{
		{
			name:       "bare site-collection GUID",
			identifier: siteCollectionID,
			expect:     true,
		},
		{
			name:       "composite id, no hostname",
			identifier: siteCollectionID + "," + webID,
			expect:     true,
		},
		{
			name:       "composite id, with hostname",
			identifier: hostname + "," + siteCollectionID + "," + webID,
			expect:     true,
		},
		{
			name:       "webURL",
			identifier: "https://" + hostname + "/sites/Example",
			expect:     false,
		},
		{
			name:       "webURL path only",
			identifier: "/sites/Example",
			expect:     false,
		},
		{
			name:       "empty string",
			identifier: "",
			expect:     false,
		},
		{
			name:       "hostname with a single trailing comma and no UUID",
			identifier: hostname + ",",
			expect:     false,
		},
		{
			name:       "too many comma-separated segments",
			identifier: hostname + "," + siteCollectionID + "," + webID + "," + webID,
			expect:     false,
		},
		{
			name:       "two-part id where one segment isn't a UUID",
			identifier: hostname + "," + siteCollectionID,
			expect:     false,
		},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			assert.Equal(t, test.expect, isSiteID(test.identifier))
		})
	}
}

type SitesIntgSuite struct {
	tester.Suite
	its intgTesterSetup
}

func TestSitesIntgSuite(t *testing.T) {
	suite.Run(t, &SitesIntgSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{tconfig.M365AcctCredEnvs}),
	})
}

func (suite *SitesIntgSuite) SetupSuite() {
	suite.its = newIntegrationTesterSetup(suite.T())
}

func (suite *SitesIntgSuite) TestGetAll() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	sites, err := suite.its.ac.
		Sites().
		GetAll(ctx, fault.New(true))
	require.NoError(t, err)
	require.NotZero(t, len(sites), "must have at least one site")

	for _, site := range sites {
		assert.NotContains(t, ptr.Val(site.GetWebUrl()), personalSitePath, "must not return onedrive sites")
		assert.NotContains(t, ptr.Val(site.GetWebUrl()), "sharepoint.com/search", "must not return search site")
	}
}

func (suite *SitesIntgSuite) TestSites_GetByID() {
	var (
		t               = suite.T()
		siteID          = tconfig.M365SiteID(t)
		parts           = strings.Split(siteID, ",")
		uuids           = siteID
		siteURL         = tconfig.M365SiteURL(t)
		modifiedSiteURL = siteURL + "foo"
	)

	if len(parts) == 3 {
		uuids = strings.Join(parts[1:], ",")
	}

	sitesAPI := suite.its.ac.Sites()

	table := []struct {
		name      string
		id        string
		expectErr func(*testing.T, error) bool
	}{
		{
			name: "3 part id",
			id:   siteID,
			expectErr: func(t *testing.T, err error) bool {
				assert.NoError(t, err, clues.ToCore(err))
				return false
			},
		},
		{
			name: "2 part id",
			id:   uuids,
			expectErr: func(t *testing.T, err error) bool {
				assert.NoError(t, err, clues.ToCore(err))
				return false
			},
		},
		{
			// A bare site-collection GUID with no comma at all. Before the
			// fix to isSiteID (see CHANGELOG.md), this fell through to the
			// webURL-parsing branch and Graph rejected the resulting
			// nonsense hostname with some error, which this test could not
			// distinguish from a genuine "no such site" response. Asserting
			// ErrNotFound specifically proves the identifier is now
			// dispatched through Sites().BySiteId(...) rather than the
			// webURL branch.
			name: "bare id, no such site",
			id:   uuid.NewString(),
			expectErr: func(t *testing.T, err error) bool {
				assert.ErrorIs(t, err, core.ErrNotFound, clues.ToCore(err))
				return true
			},
		},
		{
			name: "random id",
			id:   uuid.NewString() + "," + uuid.NewString(),
			expectErr: func(t *testing.T, err error) bool {
				assert.ErrorIs(t, err, core.ErrNotFound, clues.ToCore(err))
				return true
			},
		},
		{
			name: "url",
			id:   siteURL,
			expectErr: func(t *testing.T, err error) bool {
				assert.NoError(t, err, clues.ToCore(err))
				return false
			},
		},
		{
			name: "malformed url",
			id:   "barunihlda",
			expectErr: func(t *testing.T, err error) bool {
				assert.Error(t, err, clues.ToCore(err))
				return true
			},
		},
		{
			name: "well formed url, invalid hostname",
			id:   "https://test/sites/testing",
			expectErr: func(t *testing.T, err error) bool {
				assert.Error(t, err, clues.ToCore(err))
				return true
			},
		},
		{
			name: "well formed url, no sites match",
			id:   modifiedSiteURL,
			expectErr: func(t *testing.T, err error) bool {
				assert.ErrorIs(t, err, core.ErrNotFound, clues.ToCore(err))
				return true
			},
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			cc := CallConfig{
				Expand: []string{"drive"},
			}

			site, err := sitesAPI.GetByID(ctx, test.id, cc)
			expectedErr := test.expectErr(t, err)

			if expectedErr {
				return
			}

			require.NotEmpty(t, ptr.Val(site.GetId()), "must have an id")
			require.NotNil(t, site.GetDrive(), "must have drive info")
			require.NotNil(t, site.GetDrive().GetOwner(), "must have drive owner info")
		})
	}
}

func (suite *SitesIntgSuite) TestGetRoot() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	result, err := suite.its.ac.Sites().GetRoot(ctx, CallConfig{Expand: []string{"drive"}})
	require.NoError(t, err)
	require.NotNil(t, result, "must find the root site")
	require.NotEmpty(t, ptr.Val(result.GetId()), "must have an id")
	require.NotNil(t, result.GetDrive(), "must have drive info")
	require.NotNil(t, result.GetDrive().GetOwner(), "must have drive owner info")
}
