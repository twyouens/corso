package seed

import (
	"context"
	"fmt"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// libraryName and listName are the single dedicated document library and
// dedicated list the seeder owns on the seeded site. The site itself
// pre-exists and is never created, deleted or otherwise modified by the
// seeder beyond adding these two child objects.
const (
	libraryName = Prefix + "-library"
	listName    = Prefix + "-list"
)

// libraryFiles is the fixed, known content the seeder writes into the
// document library's root folder.
var libraryFiles = []struct {
	name    string
	content string
}{
	{name: Prefix + "-doc-001.txt", content: "k3ep seed harness SharePoint document 1\n"},
}

// listItemTitles is the fixed, known content the seeder writes into the
// list. Each title is its own single-column ("Title") item; that single
// field is all the seeder needs to prove listing a seeded list matches the
// manifest.
var listItemTitles = []string{
	Prefix + "-list-item-001",
	Prefix + "-list-item-002",
}

// seedSharePoint idempotently ensures the dedicated document library (with
// its known files) and the dedicated list (with its known items) exist on
// siteID, and returns a manifest item for each.
func seedSharePoint(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, error) {
	libraryItems, err := seedDocumentLibrary(ctx, c, siteID)
	if err != nil {
		return nil, clues.Wrap(err, "seeding document library")
	}

	listItems, err := seedList(ctx, c, siteID)
	if err != nil {
		return nil, clues.Wrap(err, "seeding list")
	}

	return append(libraryItems, listItems...), nil
}

func seedDocumentLibrary(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, error) {
	lists := c.Lists()
	drives := c.Drives()

	driveID, _, err := FindOrCreate(
		func() (string, bool, error) {
			found, err := findListByDisplayName(ctx, lists, siteID, libraryName)
			if err != nil {
				return "", false, clues.Wrap(err, "looking up seed document library")
			}

			if found == "" {
				return "", false, nil
			}

			drive, err := lists.Stable.
				Client().
				Sites().
				BySiteId(siteID).
				Lists().
				ByListId(found).
				Drive().
				Get(ctx, nil)
			if err != nil {
				return "", false, clues.Wrap(err, "fetching seed document library drive")
			}

			return ptr.Val(drive.GetId()), true, nil
		},
		func() (string, error) {
			drive, err := lists.PostDrive(ctx, siteID, libraryName)
			if err != nil {
				return "", clues.Wrap(err, "creating seed document library")
			}

			return ptr.Val(drive.GetId()), nil
		})
	if err != nil {
		return nil, err
	}

	root, err := drives.GetRootFolder(ctx, driveID)
	if err != nil {
		return nil, clues.Wrap(err, "getting seed document library root")
	}

	rootID := ptr.Val(root.GetId())

	existingChildren, err := drives.GetFolderChildren(ctx, driveID, rootID)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed document library contents")
	}

	existingByName := map[string]string{}
	for _, child := range existingChildren {
		existingByName[ptr.Val(child.GetName())] = ptr.Val(child.GetId())
	}

	items := []ManifestItem{
		{
			// The library is implemented on the same Drives client as
			// OneDrive (a document library is just another drive), but it
			// is its own workload for manifest purposes.
			Workload:   WorkloadSharePointLibrary,
			Kind:       "library",
			ID:         driveID,
			Name:       libraryName,
			ResourceID: siteID,
		},
	}

	for _, want := range libraryFiles {
		fileID, _, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existingByName[want.name]
				return id, found, nil
			},
			func() (string, error) {
				newFile := api.NewDriveItem(want.name, false)

				created, err := drives.PostItemInContainer(ctx, driveID, rootID, newFile, control.Skip)
				if AlreadyExists(err) {
					refreshed, lookupErr := drives.GetFolderChildren(ctx, driveID, rootID)
					if lookupErr != nil {
						return "", clues.Wrap(lookupErr, "re-listing seed document library after conflict")
					}

					for _, child := range refreshed {
						if ptr.Val(child.GetName()) == want.name {
							return ptr.Val(child.GetId()), nil
						}
					}

					return "", clues.New(fmt.Sprintf("seed document %q reported a conflict but was not found", want.name))
				}

				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed document %q", want.name))
				}

				id := ptr.Val(created.GetId())

				if err := drives.PutItemContent(ctx, driveID, id, []byte(want.content)); err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("uploading content for seed document %q", want.name))
				}

				return id, nil
			})
		if err != nil {
			return nil, err
		}

		items = append(items, ManifestItem{
			Workload:   WorkloadSharePointLibrary,
			Kind:       "file",
			ID:         fileID,
			Name:       want.name,
			FolderPath: libraryName,
			SizeBytes:  int64(len(want.content)),
			ResourceID: siteID,
		})
	}

	return items, nil
}

func seedList(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, error) {
	lists := c.Lists()

	listID, _, err := FindOrCreate(
		func() (string, bool, error) {
			found, err := findListByDisplayName(ctx, lists, siteID, listName)
			if err != nil {
				return "", false, clues.Wrap(err, "looking up seed list")
			}

			return found, found != "", nil
		},
		func() (string, error) {
			list := models.NewList()
			list.SetDisplayName(ptr.To(listName))
			list.SetDescription(ptr.To("k3ep seed harness test data; see docs/test-tenant-harness.md"))

			info := models.NewListInfo()
			info.SetTemplate(ptr.To("genericList"))
			list.SetList(info)

			created, err := lists.Stable.
				Client().
				Sites().
				BySiteId(siteID).
				Lists().
				Post(ctx, list, nil)
			if err != nil {
				return "", clues.Wrap(err, "creating seed list")
			}

			return ptr.Val(created.GetId()), nil
		})
	if err != nil {
		return nil, err
	}

	existingTitles, err := listItemIDsByTitle(ctx, lists, siteID, listID)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed list contents")
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadSharePointList,
			Kind:       "list",
			ID:         listID,
			Name:       listName,
			ResourceID: siteID,
		},
	}

	for _, title := range listItemTitles {
		itemID, _, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existingTitles[title]
				return id, found, nil
			},
			func() (string, error) {
				fields := models.NewFieldValueSet()
				fields.SetAdditionalData(map[string]any{"Title": title})

				li := models.NewListItem()
				li.SetFields(fields)

				// Posting through api.Lists.PostListItems and then re-listing
				// to find the new item's ID (as an earlier version of this
				// function did) is unreliable: a live run against the real
				// test tenant proved that re-querying immediately after
				// creation can miss the just-written item - SharePoint list
				// item field indexing is eventually, not immediately,
				// consistent. Posting directly and reading the ID straight
				// off the creation response avoids that race entirely.
				created, err := lists.Stable.
					Client().
					Sites().
					BySiteId(siteID).
					Lists().
					ByListId(listID).
					Items().
					Post(ctx, li, nil)
				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed list item %q", title))
				}

				return ptr.Val(created.GetId()), nil
			})
		if err != nil {
			return nil, err
		}

		items = append(items, ManifestItem{
			Workload:   WorkloadSharePointList,
			Kind:       "list_item",
			ID:         itemID,
			Name:       title,
			FolderPath: listName,
			ResourceID: siteID,
		})
	}

	return items, nil
}

// findListByDisplayName returns the ID of the list on siteID whose
// DisplayName matches name, or "" if none matches. api.Lists has no
// "find list by name" wrapper (only GetListByID, which needs an ID the
// seeder doesn't have yet on a fresh tenant), so this lists every list on
// the site and matches client-side - sites rarely have more than a
// handful, so this is cheap.
func findListByDisplayName(
	ctx context.Context,
	lists api.Lists,
	siteID, name string,
) (string, error) {
	resp, err := lists.Stable.
		Client().
		Sites().
		BySiteId(siteID).
		Lists().
		Get(ctx, nil)
	if err != nil {
		return "", clues.Wrap(err, "listing site lists")
	}

	for _, l := range resp.GetValue() {
		if ptr.Val(l.GetDisplayName()) == name {
			return ptr.Val(l.GetId()), nil
		}
	}

	return "", nil
}

// listItemIDsByTitle returns a Title->id map of every item currently in
// listID, by fetching each item's Fields sub-resource directly (the public
// GetListItems wrapper does not populate Fields; only the package-private
// getListItemFields does, so this mirrors that call through the same
// exported Stable escape hatch used elsewhere in this file).
func listItemIDsByTitle(
	ctx context.Context,
	lists api.Lists,
	siteID, listID string,
) (map[string]string, error) {
	existing, err := lists.GetListItems(ctx, siteID, listID, api.CallConfig{})
	if err != nil {
		return nil, clues.Wrap(err, "getting list items")
	}

	out := map[string]string{}

	for _, li := range existing {
		id := ptr.Val(li.GetId())

		fields, err := lists.Stable.
			Client().
			Sites().
			BySiteId(siteID).
			Lists().
			ByListId(listID).
			Items().
			ByListItemId(id).
			Fields().
			Get(ctx, nil)
		if err != nil {
			return nil, clues.Wrap(err, "getting list item fields")
		}

		if title, ok := stringAdditionalDataValue(fields.GetAdditionalData()["Title"]); ok {
			out[title] = id
		}
	}

	return out, nil
}

// stringAdditionalDataValue extracts a string value from a Kiota
// AdditionalData map entry. A live run against the real test tenant proved
// this cannot be a plain type assertion to string: Kiota deserialises
// recognised scalar values in AdditionalData as pointers (*string here, for
// Title), not the bare value, so `v.(string)` always failed, the "already
// seeded" check in listItemIDsByTitle was always empty, and every seeder run
// created a fresh duplicate list item instead of finding the one from the
// run before. This helper accepts either shape.
func stringAdditionalDataValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case *string:
		if t == nil {
			return "", false
		}

		return *t, true
	default:
		return "", false
	}
}
