package seed

import (
	"context"
	"fmt"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// contactsFolderName is the single dedicated contacts folder the seeder
// owns in the seeded user's mailbox.
const contactsFolderName = Prefix + "-contacts"

// contactsToSeed is the fixed, known content the seeder writes.
var contactsToSeed = []struct {
	displayName string
	givenName   string
	surname     string
	email       string
}{
	{
		displayName: Prefix + "-contact-001",
		givenName:   "K3ep",
		surname:     "Seed One",
		email:       "k3ep-seed-contact-001@example.com",
	},
}

// seedContacts idempotently ensures the dedicated contacts folder and its
// known contacts exist in userID's mailbox, and returns a manifest item for
// each.
func seedContacts(ctx context.Context, c api.Client, userID string) ([]ManifestItem, error) {
	contacts := c.Contacts()

	folder, _, err := FindOrCreate(
		func() (graph.Container, bool, error) {
			found, err := contacts.GetContainerByName(ctx, userID, "", contactsFolderName)
			if err == nil {
				return found, true, nil
			}

			if ContainerNotFound(err) {
				return nil, false, nil
			}

			return nil, false, clues.Wrap(err, "looking up seed contacts folder")
		},
		func() (graph.Container, error) {
			created, err := contacts.CreateContainer(ctx, userID, "", contactsFolderName)
			return created, clues.Wrap(err, "creating seed contacts folder").OrNil()
		})
	if err != nil {
		return nil, err
	}

	folderID := ptr.Val(folder.GetId())

	existing, err := listContactIDsByDisplayName(ctx, contacts, userID, folderID)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed contacts folder contents")
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadExchangeContacts,
			Kind:       "folder",
			ID:         folderID,
			Name:       contactsFolderName,
			ResourceID: userID,
		},
	}

	for _, want := range contactsToSeed {
		contactID, _, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existing[want.displayName]
				return id, found, nil
			},
			func() (string, error) {
				contact := models.NewContact()
				contact.SetDisplayName(ptr.To(want.displayName))
				contact.SetGivenName(ptr.To(want.givenName))
				contact.SetSurname(ptr.To(want.surname))

				email := models.NewEmailAddress()
				email.SetAddress(ptr.To(want.email))
				email.SetName(ptr.To(want.displayName))
				contact.SetEmailAddresses([]models.EmailAddressable{email})

				posted, err := contacts.PostItem(ctx, userID, folderID, contact)
				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed contact %q", want.displayName))
				}

				return ptr.Val(posted.GetId()), nil
			})
		if err != nil {
			return nil, err
		}

		items = append(items, ManifestItem{
			Workload:   WorkloadExchangeContacts,
			Kind:       "contact",
			ID:         contactID,
			Name:       want.displayName,
			FolderPath: contactsFolderName,
			ResourceID: userID,
		})
	}

	return items, nil
}

// listContactIDsByDisplayName returns a displayName->id map of the contacts
// already in folderID. api.Contacts has no "list contacts" wrapper (only
// GetItem-by-ID and PostItem), so this uses the same raw-client escape
// hatch as listMessageIDsBySubject/listEventIDsBySubject.
func listContactIDsByDisplayName(
	ctx context.Context,
	contacts api.Contacts,
	userID, folderID string,
) (map[string]string, error) {
	resp, err := contacts.Stable.
		Client().
		Users().
		ByUserId(userID).
		ContactFolders().
		ByContactFolderId(folderID).
		Contacts().
		Get(ctx, &users.ItemContactFoldersItemContactsRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemContactFoldersItemContactsRequestBuilderGetQueryParameters{
				Select: []string{"id", "displayName"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing contacts")
	}

	out := map[string]string{}

	for _, contact := range resp.GetValue() {
		out[ptr.Val(contact.GetDisplayName())] = ptr.Val(contact.GetId())
	}

	return out, nil
}
