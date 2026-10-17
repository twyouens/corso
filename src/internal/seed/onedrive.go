package seed

import (
	"context"
	"errors"
	"fmt"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// oneDriveFolderName is the single dedicated top-level folder the seeder
// owns in the seeded user's OneDrive.
const oneDriveFolderName = Prefix

// oneDriveFiles is the fixed, known content the seeder writes.
var oneDriveFiles = []struct {
	name    string
	content string
}{
	{name: Prefix + "-file-001.txt", content: "k3ep seed harness OneDrive file 1\n"},
	{name: Prefix + "-file-002.txt", content: "k3ep seed harness OneDrive file 2, a little longer.\n"},
}

// seedOneDrive idempotently ensures the dedicated folder and its known
// files exist in userID's OneDrive, and returns a manifest item for each
// (including the drive-owning folder itself).
func seedOneDrive(ctx context.Context, c api.Client, userID string) ([]ManifestItem, error) {
	drive, err := c.Users().GetDefaultDrive(ctx, userID)
	if err != nil {
		return nil, clues.Wrap(err, "getting user's OneDrive")
	}

	driveID := ptr.Val(drive.GetId())
	drives := c.Drives()

	root, err := drives.GetRootFolder(ctx, driveID)
	if err != nil {
		return nil, clues.Wrap(err, "getting OneDrive root")
	}

	rootID := ptr.Val(root.GetId())

	folder, _, err := FindOrCreate(
		func() (string, bool, error) {
			found, err := drives.GetFolderByName(ctx, driveID, rootID, oneDriveFolderName)
			if err == nil {
				return ptr.Val(found.GetId()), true, nil
			}

			if errors.Is(err, api.ErrFolderNotFound) {
				return "", false, nil
			}

			return "", false, clues.Wrap(err, "looking up seed OneDrive folder")
		},
		func() (string, error) {
			newFolder := api.NewDriveItem(oneDriveFolderName, true)

			created, err := drives.PostItemInContainer(ctx, driveID, rootID, newFolder, control.Skip)
			if AlreadyExists(err) {
				// lost a race with another run; the folder exists now, use it.
				found, lookupErr := drives.GetFolderByName(ctx, driveID, rootID, oneDriveFolderName)
				if lookupErr != nil {
					return "", clues.Wrap(lookupErr, "re-fetching seed OneDrive folder after conflict")
				}

				return ptr.Val(found.GetId()), nil
			}

			if err != nil {
				return "", clues.Wrap(err, "creating seed OneDrive folder")
			}

			return ptr.Val(created.GetId()), nil
		})
	if err != nil {
		return nil, err
	}

	existingChildren, err := drives.GetFolderChildren(ctx, driveID, folder)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed OneDrive folder contents")
	}

	existingByName := map[string]string{}
	for _, child := range existingChildren {
		existingByName[ptr.Val(child.GetName())] = ptr.Val(child.GetId())
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadOneDrive,
			Kind:       "folder",
			ID:         folder,
			Name:       oneDriveFolderName,
			ResourceID: driveID,
		},
	}

	for _, want := range oneDriveFiles {
		fileID, _, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existingByName[want.name]
				return id, found, nil
			},
			func() (string, error) {
				newFile := api.NewDriveItem(want.name, false)

				created, err := drives.PostItemInContainer(ctx, driveID, folder, newFile, control.Skip)
				if AlreadyExists(err) {
					// another run created it first; the current content is
					// already correct (seed content is fixed), so this id
					// just needs to be found, not overwritten.
					refreshed, lookupErr := drives.GetFolderChildren(ctx, driveID, folder)
					if lookupErr != nil {
						return "", clues.Wrap(lookupErr, "re-listing seed OneDrive folder after conflict")
					}

					for _, child := range refreshed {
						if ptr.Val(child.GetName()) == want.name {
							return ptr.Val(child.GetId()), nil
						}
					}

					return "", clues.New(fmt.Sprintf("seed OneDrive file %q reported a conflict but was not found", want.name))
				}

				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed OneDrive file %q", want.name))
				}

				id := ptr.Val(created.GetId())

				if err := drives.PutItemContent(ctx, driveID, id, []byte(want.content)); err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("uploading content for seed OneDrive file %q", want.name))
				}

				return id, nil
			})
		if err != nil {
			return nil, err
		}

		items = append(items, ManifestItem{
			Workload:   WorkloadOneDrive,
			Kind:       "file",
			ID:         fileID,
			Name:       want.name,
			FolderPath: oneDriveFolderName,
			SizeBytes:  int64(len(want.content)),
			ResourceID: driveID,
		})
	}

	return items, nil
}
