package seed

import (
	"context"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// Run seeds every v1 workload (Exchange mail, calendar, contacts; OneDrive;
// SharePoint document library and list) into the tenant identified by c and
// cfg, idempotently, then writes the resulting manifest to
// cfg.ManifestPath and returns it.
//
// Run never deletes anything. Each workload's seed* function finds-or-
// creates its own dedicated, Prefix-named containers and items; calling Run
// again leaves everything an earlier run created exactly as it was.
func Run(ctx context.Context, c api.Client, cfg Config) (Manifest, error) {
	var all []ManifestItem

	mail, err := seedMail(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange mail")
	}

	all = append(all, mail...)

	calendar, err := seedCalendar(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange calendar")
	}

	all = append(all, calendar...)

	contacts, err := seedContacts(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding exchange contacts")
	}

	all = append(all, contacts...)

	oneDrive, err := seedOneDrive(ctx, c, cfg.UserID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding onedrive")
	}

	all = append(all, oneDrive...)

	sharePoint, err := seedSharePoint(ctx, c, cfg.SiteID)
	if err != nil {
		return Manifest{}, clues.Wrap(err, "seeding sharepoint")
	}

	all = append(all, sharePoint...)

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
