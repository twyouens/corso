package seed

import (
	"context"
	"fmt"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// WorkloadSummary tallies, for one workload, how many items the seeder
// found already present versus how many it had to create. This is the
// "found versus created" accounting a human re-running the seeder or the
// smoke test can check against a reported result, without this package
// ever logging an item's name, subject, filename or any credential -
// counts and workload names only.
type WorkloadSummary struct {
	Workload Workload
	Found    int
	Created  int
}

// record tallies one FindOrCreate outcome into the summary.
func (s *WorkloadSummary) record(created bool) {
	if created {
		s.Created++
	} else {
		s.Found++
	}
}

func (s WorkloadSummary) String() string {
	return fmt.Sprintf("%s: %d found, %d created", s.Workload, s.Found, s.Created)
}

// Run seeds every v1 workload (Exchange mail, calendar, contacts; OneDrive;
// SharePoint document library and list) into the tenant identified by c and
// cfg, idempotently, then writes the resulting manifest to
// cfg.ManifestPath and returns it.
//
// Run never deletes anything. Each workload's seed* function finds-or-
// creates its own dedicated, Prefix-named containers and items; calling Run
// again leaves everything an earlier run created exactly as it was.
//
// report, if non-nil, receives one line at a time: the resolved target
// user and site (name/UPN and ID, never a credential) before any write,
// and a found-versus-created tally per workload afterwards. Like every
// other long-running operation in this fork, Run itself never writes to
// stdout or a log file - the caller decides where these lines go.
// src/cmd/seed prints them; the integration test writes them to the test
// log via t.Log.
func Run(ctx context.Context, c api.Client, cfg Config, report func(string)) (Manifest, error) {
	if report == nil {
		report = func(string) {}
	}

	_, userName, err := c.Users().GetIDAndName(ctx, cfg.UserID, api.CallConfig{})
	if err != nil {
		return Manifest{}, clues.Wrap(err, "resolving seed target user")
	}

	report(fmt.Sprintf("target user: %s (%s)", userName, cfg.UserID))

	siteName, err := resolveSiteName(ctx, c, cfg.SiteID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "resolving seed target site")
	}

	report(fmt.Sprintf("target site: %s (%s)", siteName, cfg.SiteID))

	var all []ManifestItem

	mail, mailSummary, err := seedMail(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange mail")
	}

	all = append(all, mail...)
	report(mailSummary.String())

	calendar, calSummary, err := seedCalendar(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange calendar")
	}

	all = append(all, calendar...)
	report(calSummary.String())

	contacts, contactsSummary, err := seedContacts(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange contacts")
	}

	all = append(all, contacts...)
	report(contactsSummary.String())

	oneDrive, oneDriveSummary, err := seedOneDrive(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding onedrive")
	}

	all = append(all, oneDrive...)
	report(oneDriveSummary.String())

	sharePoint, sharePointSummaries, err := seedSharePoint(ctx, c, cfg.SiteID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding sharepoint")
	}

	all = append(all, sharePoint...)

	for _, s := range sharePointSummaries {
		report(s.String())
	}

	manifest := NewManifest(cfg.TenantID, all)

	manifestPath := cfg.ManifestPath
	if manifestPath == "" {
		manifestPath = DefaultManifestPath
	}

	if err := manifest.Write(manifestPath); err != nil {
		return Manifest{}, clues.Wrap(err, "writing seed manifest")
	}

	return manifest, nil
}

// resolveSiteName returns siteID's display name (falling back to its web
// URL if the display name is empty), for the "resolved target identity"
// line Run reports before any write.
//
// This does not call api.Sites.GetIDAndName/GetByID, even though that
// looks like the obviously right wrapper to use. A live run proved it
// fails for a site ID shaped like the one this harness was actually
// given: GetByID dispatches on a regex (siteIDRE in
// pkg/services/m365/api/sites.go) that requires at least two comma-
// separated UUIDs (the "hostname,siteCollectionId,webId" composite form).
// A bare site-collection GUID with no commas - exactly what SEED_SITE_ID
// is here, and exactly what every other call in this package already
// passes to Sites().BySiteId(...) successfully - fails that regex, so
// GetByID falls through to its "treat it as a URL" branch and sends a
// nonsense hostname, which Graph rejects with "Invalid hostname for this
// tenancy". That is a real bug in existing, shared code outside this
// package; fixing the regex there is a broader change with other callers
// to consider, so this works around it locally by reaching the same
// Sites().BySiteId(...) escape hatch sharepoint.go already uses
// successfully with this exact ID, rather than widening sites.go's
// dispatch logic as part of this chunk.
func resolveSiteName(ctx context.Context, c api.Client, siteID string) (string, error) {
	site, err := c.Sites().Stable.
		Client().
		Sites().
		BySiteId(siteID).
		Get(ctx, nil)
	if err != nil {
		return "", clues.Wrap(err, "getting site")
	}

	name := ptr.Val(site.GetDisplayName())
	if name == "" {
		name = ptr.Val(site.GetWebUrl())
	}

	return name, nil
}
