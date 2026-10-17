package seed

import (
	"context"
	"errors"
	"fmt"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// oneDriveFolderName is the single dedicated top-level folder the seeder
// owns in the seeded user's OneDrive.
const oneDriveFolderName = Prefix

// oneDriveFiles is the fixed, known content the seeder writes.
//
// k3ep-seed-file-003.txt was added deliberately, not as scope creep: every
// other item here already existed in the test tenant by the time
// CreatedAt/ModifiedAt were wired up to real Graph values, so every live
// run only ever exercised the "found" branch. A third, permanent file gives
// one real create-then-found transition to prove live, the way the review
// asked for - it stays part of this workload's seed set going forward,
// the same as the other two.
var oneDriveFiles = []struct {
	name    string
	content string
}{
	{name: Prefix + "-file-001.txt", content: "k3ep seed harness OneDrive file 1\n"},
	{name: Prefix + "-file-002.txt", content: "k3ep seed harness OneDrive file 2, a little longer.\n"},
	{name: Prefix + "-file-003.txt", content: "k3ep seed harness OneDrive file 3.\n"},
}

// seedOneDrive idempotently ensures the dedicated folder and its known
// files exist in userID's OneDrive, and returns a manifest item for each
// (including the drive-owning folder itself), plus how many of each were
// found versus freshly created.
func seedOneDrive(ctx context.Context, c api.Client, userID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadOneDrive}

	drive, err := c.Users().GetDefaultDrive(ctx, userID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "getting user's OneDrive")
	}

	driveID := ptr.Val(drive.GetId())
	drives := c.Drives()

	root, err := drives.GetRootFolder(ctx, driveID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "getting OneDrive root")
	}

	rootID := ptr.Val(root.GetId())

	folder, folderCreated, err := FindOrCreate(
		func() (models.DriveItemable, bool, error) {
			found, err := drives.GetFolderByName(ctx, driveID, rootID, oneDriveFolderName)
			if err == nil {
				return found, true, nil
			}

			if errors.Is(err, api.ErrFolderNotFound) {
				return nil, false, nil
			}

			return nil, false, clues.Wrap(err, "looking up seed OneDrive folder")
		},
		func() (models.DriveItemable, error) {
			newFolder := api.NewDriveItem(oneDriveFolderName, true)

			created, err := drives.PostItemInContainer(ctx, driveID, rootID, newFolder, control.Skip)
			if AlreadyExists(err) {
				// lost a race with another run; the folder exists now, use it.
				found, lookupErr := drives.GetFolderByName(ctx, driveID, rootID, oneDriveFolderName)
				if lookupErr != nil {
					return nil, clues.Wrap(lookupErr, "re-fetching seed OneDrive folder after conflict")
				}

				return found, nil
			}

			if err != nil {
				return nil, clues.Wrap(err, "creating seed OneDrive folder")
			}

			return created, nil
		})
	if err != nil {
		return nil, summary, err
	}

	summary.record(folderCreated)

	folderID := ptr.Val(folder.GetId())

	existingChildren, err := drives.GetFolderChildren(ctx, driveID, folderID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed OneDrive folder contents")
	}

	existingByName := driveItemsByName(existingChildren)

	items := []ManifestItem{
		datedManifestItem(
			folder,
			WorkloadOneDrive,
			"folder",
			oneDriveFolderName,
			"",
			0,
			driveID),
	}

	for _, want := range oneDriveFiles {
		file, fileCreated, err := FindOrCreate(
			func() (models.DriveItemable, bool, error) {
				f, found := existingByName[want.name]
				return f, found, nil
			},
			func() (models.DriveItemable, error) {
				newFile := api.NewDriveItem(want.name, false)

				created, err := drives.PostItemInContainer(ctx, driveID, folderID, newFile, control.Skip)
				if AlreadyExists(err) {
					// another run created it first; the current content is
					// already correct (seed content is fixed), so this just
					// needs to be found, not overwritten.
					refreshed, lookupErr := drives.GetFolderChildren(ctx, driveID, folderID)
					if lookupErr != nil {
						return nil, clues.Wrap(lookupErr, "re-listing seed OneDrive folder after conflict")
					}

					for _, child := range refreshed {
						if ptr.Val(child.GetName()) == want.name {
							return child, nil
						}
					}

					return nil, clues.New(fmt.Sprintf("seed OneDrive file %q reported a conflict but was not found", want.name))
				}

				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed OneDrive file %q", want.name))
				}

				id := ptr.Val(created.GetId())

				if err := drives.PutItemContent(ctx, driveID, id, []byte(want.content)); err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("uploading content for seed OneDrive file %q", want.name))
				}

				// created's LastModifiedDateTime is from the metadata-only
				// POST, before the content upload just above - Graph bumps
				// lastModifiedDateTime on that content write, so re-fetch
				// rather than return the stale pre-upload value. Otherwise
				// a file's ModifiedAt recorded on the creating run could
				// differ from what a later found run reads for the same
				// physical file - a narrower reintroduction of exactly the
				// found-versus-created date divergence this chunk's other
				// fixes close everywhere else.
				refetched, err := drives.GetItem(ctx, driveID, id)
				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("re-fetching seed OneDrive file %q after upload", want.name))
				}

				return refetched, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(fileCreated)

		items = append(items, datedManifestItem(
			file,
			WorkloadOneDrive,
			"file",
			want.name,
			oneDriveFolderName,
			int64(len(want.content)),
			driveID))
	}

	return items, summary, nil
}

// driveItemsByName builds a name->item map from an already-fetched slice of
// drive items (folder children). Split out from the GetFolderChildren call
// site so the map-building logic - the actual idempotency decision input -
// is unit-testable without a Graph call.
func driveItemsByName(items []models.DriveItemable) map[string]models.DriveItemable {
	out := map[string]models.DriveItemable{}

	for _, item := range items {
		out[ptr.Val(item.GetName())] = item
	}

	return out
}
