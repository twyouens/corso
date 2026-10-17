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
// siteID, and returns a manifest item for each, plus a found-versus-created
// summary for each of the two SharePoint workloads (library and list).
func seedSharePoint(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, []WorkloadSummary, error) {
	libraryItems, librarySummary, err := seedDocumentLibrary(ctx, c, siteID)
	if err != nil {
		return nil, nil, clues.Wrap(err, "seeding document library")
	}

	listItems, listSummary, err := seedList(ctx, c, siteID)
	if err != nil {
		return nil, nil, clues.Wrap(err, "seeding list")
	}

	return append(libraryItems, listItems...), []WorkloadSummary{librarySummary, listSummary}, nil
}

func seedDocumentLibrary(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadSharePointLibrary}
	lists := c.Lists()
	drives := c.Drives()

	drive, driveCreated, err := FindOrCreate(
		func() (models.Driveable, bool, error) {
			found, err := listByDisplayName(ctx, lists, siteID, libraryName)
			if err != nil {
				return nil, false, clues.Wrap(err, "looking up seed document library")
			}

			if found == nil {
				return nil, false, nil
			}

			drive, err := lists.Stable.
				Client().
				Sites().
				BySiteId(siteID).
				Lists().
				ByListId(ptr.Val(found.GetId())).
				Drive().
				Get(ctx, nil)
			if err != nil {
				return nil, false, clues.Wrap(err, "fetching seed document library drive")
			}

			return drive, true, nil
		},
		func() (models.Driveable, error) {
			drive, err := lists.PostDrive(ctx, siteID, libraryName)
			if err != nil {
				return nil, clues.Wrap(err, "creating seed document library")
			}

			return drive, nil
		})
	if err != nil {
		return nil, summary, err
	}

	summary.record(driveCreated)

	driveID := ptr.Val(drive.GetId())

	root, err := drives.GetRootFolder(ctx, driveID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "getting seed document library root")
	}

	rootID := ptr.Val(root.GetId())

	existingChildren, err := drives.GetFolderChildren(ctx, driveID, rootID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed document library contents")
	}

	existingByName := driveItemsByName(existingChildren)

	items := []ManifestItem{
		{
			// The library is implemented on the same Drives client as
			// OneDrive (a document library is just another drive), but it
			// is its own workload for manifest purposes.
			Workload:   WorkloadSharePointLibrary,
			Kind:       "library",
			ID:         driveID,
			Name:       libraryName,
			CreatedAt:  drive.GetCreatedDateTime(),
			ModifiedAt: drive.GetLastModifiedDateTime(),
			ResourceID: siteID,
		},
	}

	for _, want := range libraryFiles {
		file, fileCreated, err := FindOrCreate(
			func() (models.DriveItemable, bool, error) {
				f, found := existingByName[want.name]
				return f, found, nil
			},
			func() (models.DriveItemable, error) {
				newFile := api.NewDriveItem(want.name, false)

				created, err := drives.PostItemInContainer(ctx, driveID, rootID, newFile, control.Skip)
				if AlreadyExists(err) {
					refreshed, lookupErr := drives.GetFolderChildren(ctx, driveID, rootID)
					if lookupErr != nil {
						return nil, clues.Wrap(lookupErr, "re-listing seed document library after conflict")
					}

					for _, child := range refreshed {
						if ptr.Val(child.GetName()) == want.name {
							return child, nil
						}
					}

					return nil, clues.New(fmt.Sprintf("seed document %q reported a conflict but was not found", want.name))
				}

				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed document %q", want.name))
				}

				id := ptr.Val(created.GetId())

				if err := drives.PutItemContent(ctx, driveID, id, []byte(want.content)); err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("uploading content for seed document %q", want.name))
				}

				return created, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(fileCreated)

		items = append(items, ManifestItem{
			Workload:   WorkloadSharePointLibrary,
			Kind:       "file",
			ID:         ptr.Val(file.GetId()),
			Name:       want.name,
			FolderPath: libraryName,
			SizeBytes:  int64(len(want.content)),
			CreatedAt:  file.GetCreatedDateTime(),
			ModifiedAt: file.GetLastModifiedDateTime(),
			ResourceID: siteID,
		})
	}

	return items, summary, nil
}

func seedList(ctx context.Context, c api.Client, siteID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadSharePointList}
	lists := c.Lists()

	list, listCreated, err := FindOrCreate(
		func() (models.Listable, bool, error) {
			found, err := listByDisplayName(ctx, lists, siteID, listName)
			if err != nil {
				return nil, false, clues.Wrap(err, "looking up seed list")
			}

			return found, found != nil, nil
		},
		func() (models.Listable, error) {
			newList := models.NewList()
			newList.SetDisplayName(ptr.To(listName))
			newList.SetDescription(ptr.To("k3ep seed harness test data; see docs/test-tenant-harness.md"))

			info := models.NewListInfo()
			info.SetTemplate(ptr.To("genericList"))
			newList.SetList(info)

			created, err := lists.Stable.
				Client().
				Sites().
				BySiteId(siteID).
				Lists().
				Post(ctx, newList, nil)
			if err != nil {
				return nil, clues.Wrap(err, "creating seed list")
			}

			return created, nil
		})
	if err != nil {
		return nil, summary, err
	}

	summary.record(listCreated)

	listID := ptr.Val(list.GetId())

	existingTitles, err := listItemsByTitle(ctx, lists, siteID, listID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed list contents")
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadSharePointList,
			Kind:       "list",
			ID:         listID,
			Name:       listName,
			CreatedAt:  list.GetCreatedDateTime(),
			ModifiedAt: list.GetLastModifiedDateTime(),
			ResourceID: siteID,
		},
	}

	for _, title := range listItemTitles {
		li, liCreated, err := FindOrCreate(
			func() (models.ListItemable, bool, error) {
				item, found := existingTitles[title]
				return item, found, nil
			},
			func() (models.ListItemable, error) {
				fields := models.NewFieldValueSet()
				fields.SetAdditionalData(map[string]any{"Title": title})

				newItem := models.NewListItem()
				newItem.SetFields(fields)

				// Posting through api.Lists.PostListItems and then
				// re-listing to find the new item's ID (as an earlier
				// version of this function did) is unreliable: a live run
				// against the real test tenant proved that re-querying
				// immediately after creation can miss the just-written
				// item - SharePoint list item field indexing is
				// eventually, not immediately, consistent. Posting
				// directly and reading the ID straight off the creation
				// response avoids that race entirely.
				created, err := lists.Stable.
					Client().
					Sites().
					BySiteId(siteID).
					Lists().
					ByListId(listID).
					Items().
					Post(ctx, newItem, nil)
				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed list item %q", title))
				}

				return created, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(liCreated)

		items = append(items, ManifestItem{
			Workload:   WorkloadSharePointList,
			Kind:       "list_item",
			ID:         ptr.Val(li.GetId()),
			Name:       title,
			FolderPath: listName,
			CreatedAt:  li.GetCreatedDateTime(),
			ModifiedAt: li.GetLastModifiedDateTime(),
			ResourceID: siteID,
		})
	}

	return items, summary, nil
}

// listByDisplayName returns the list on siteID whose DisplayName matches
// name, or nil if none matches. api.Lists has no "find list by name"
// wrapper (only GetListByID, which needs an ID the seeder doesn't have yet
// on a fresh tenant), so this lists every list on the site and matches
// client-side - sites rarely have more than a handful, so this is cheap.
// Returning the full object (rather than just its ID, as an earlier
// version of this function did) is what lets seedDocumentLibrary/seedList
// read real Graph dates on the "found" path, not just the "created" one.
func listByDisplayName(
	ctx context.Context,
	lists api.Lists,
	siteID, name string,
) (models.Listable, error) {
	resp, err := lists.Stable.
		Client().
		Sites().
		BySiteId(siteID).
		Lists().
		Get(ctx, nil)
	if err != nil {
		return nil, clues.Wrap(err, "listing site lists")
	}

	return listMatchingDisplayName(resp.GetValue(), name), nil
}

// listMatchingDisplayName returns the first list in lists whose
// DisplayName equals name, or nil. Split out from listByDisplayName so the
// matching logic - the actual idempotency decision input - is
// unit-testable without a Graph call.
func listMatchingDisplayName(lists []models.Listable, name string) models.Listable {
	for _, l := range lists {
		if ptr.Val(l.GetDisplayName()) == name {
			return l
		}
	}

	return nil
}

// listItemsByTitle returns a Title->item map of every item currently in
// listID, by fetching each item's Fields sub-resource directly (the public
// GetListItems wrapper does not populate Fields; only the package-private
// getListItemFields does, so this mirrors that call through the same
// exported Stable escape hatch used elsewhere in this file). The returned
// ListItemable comes from GetListItems itself, which does carry
// createdDateTime/lastModifiedDateTime, so both the found and the created
// path in seedList read real Graph dates.
func listItemsByTitle(
	ctx context.Context,
	lists api.Lists,
	siteID, listID string,
) (map[string]models.ListItemable, error) {
	existing, err := lists.GetListItems(ctx, siteID, listID, api.CallConfig{})
	if err != nil {
		return nil, clues.Wrap(err, "getting list items")
	}

	out := map[string]models.ListItemable{}

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
			out[title] = li
		}
	}

	return out, nil
}

// stringAdditionalDataValue extracts a string value from a Kiota
// AdditionalData map entry. A live run against the real test tenant proved
// this cannot be a plain type assertion to string: Kiota deserialises
// recognised scalar values in AdditionalData as pointers (*string here, for
// Title), not the bare value, so `v.(string)` always failed, the "already
// seeded" check in listItemsByTitle was always empty, and every seeder run
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
