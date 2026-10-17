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
// each, plus how many of each were found versus freshly created.
func seedContacts(ctx context.Context, c api.Client, userID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadExchangeContacts}
	contacts := c.Contacts()

	folder, folderCreated, err := FindOrCreate(
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
		return nil, summary, err
	}

	summary.record(folderCreated)

	folderID := ptr.Val(folder.GetId())

	// ContactFolder, like MailFolder and Calendar, has no
	// createdDateTime/lastModifiedDateTime in Graph at all (its model
	// embeds Entity, not OutlookItem), so the folder's manifest item below
	// is correctly dateless; there is nothing to read.
	existing, err := listContactsByDisplayName(ctx, contacts, userID, folderID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed contacts folder contents")
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
		contact, contactCreated, err := FindOrCreate(
			func() (models.Contactable, bool, error) {
				ct, found := existing[want.displayName]
				return ct, found, nil
			},
			func() (models.Contactable, error) {
				newContact := models.NewContact()
				newContact.SetDisplayName(ptr.To(want.displayName))
				newContact.SetGivenName(ptr.To(want.givenName))
				newContact.SetSurname(ptr.To(want.surname))

				email := models.NewEmailAddress()
				email.SetAddress(ptr.To(want.email))
				email.SetName(ptr.To(want.displayName))
				newContact.SetEmailAddresses([]models.EmailAddressable{email})

				posted, err := contacts.PostItem(ctx, userID, folderID, newContact)
				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed contact %q", want.displayName))
				}

				return posted, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(contactCreated)

		items = append(items, ManifestItem{
			Workload:   WorkloadExchangeContacts,
			Kind:       "contact",
			ID:         ptr.Val(contact.GetId()),
			Name:       want.displayName,
			FolderPath: contactsFolderName,
			CreatedAt:  contact.GetCreatedDateTime(),
			ModifiedAt: contact.GetLastModifiedDateTime(),
			ResourceID: userID,
		})
	}

	return items, summary, nil
}

// contactsByDisplayName builds a displayName->contact map from an
// already-fetched slice of contacts. Split out from listContactsByDisplayName
// so the map-building logic - the actual idempotency decision input - is
// unit-testable without a Graph call.
func contactsByDisplayName(contacts []models.Contactable) map[string]models.Contactable {
	out := map[string]models.Contactable{}

	for _, contact := range contacts {
		out[ptr.Val(contact.GetDisplayName())] = contact
	}

	return out
}

// listContactsByDisplayName returns a displayName->contact map of the
// contacts already in folderID, so seedContacts can decide per-contact
// whether to create it, and - whether found or freshly created - read the
// same real Graph dates either way. api.Contacts has no "list contacts"
// wrapper (only GetItem-by-ID and PostItem), so this uses the same
// raw-client escape hatch as listMessagesBySubject/listEventsBySubject.
func listContactsByDisplayName(
	ctx context.Context,
	contacts api.Contacts,
	userID, folderID string,
) (map[string]models.Contactable, error) {
	resp, err := contacts.Stable.
		Client().
		Users().
		ByUserId(userID).
		ContactFolders().
		ByContactFolderId(folderID).
		Contacts().
		Get(ctx, &users.ItemContactFoldersItemContactsRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemContactFoldersItemContactsRequestBuilderGetQueryParameters{
				Select: []string{"id", "displayName", "createdDateTime", "lastModifiedDateTime"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing contacts")
	}

	return contactsByDisplayName(resp.GetValue()), nil
}
