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

// mailFolderName is the single, dedicated top-level mail folder the seeder
// owns in the seeded user's mailbox. Everything the seeder puts in
// Exchange mail lives inside it, so the seeder never has to touch (or risk
// touching) Inbox, Sent Items or any other folder it did not create.
const mailFolderName = Prefix + "-mail"

// mailMessages is the fixed, known content the seeder writes. Fixed content
// is what makes the seeder's idempotency simple: because the desired state
// never changes between runs, "found by subject" is always "already
// correct" - there is no drift to detect or patch, only existence to check.
var mailMessages = []struct {
	subject string
	body    string
}{
	{subject: Prefix + "-mail-001", body: "k3ep seed harness test message 1"},
	{subject: Prefix + "-mail-002", body: "k3ep seed harness test message 2"},
}

// seedMail idempotently ensures the dedicated mail folder and its known
// messages exist in userID's mailbox, and returns a manifest item for
// each, plus how many of each were found versus freshly created.
func seedMail(ctx context.Context, c api.Client, userID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadExchangeMail}
	mail := c.Mail()

	folder, folderCreated, err := FindOrCreate(
		func() (graph.Container, bool, error) {
			found, err := mail.GetContainerByName(ctx, userID, "", mailFolderName)
			if err == nil {
				return found, true, nil
			}

			if ContainerNotFound(err) {
				return nil, false, nil
			}

			return nil, false, clues.Wrap(err, "looking up seed mail folder")
		},
		func() (graph.Container, error) {
			created, err := mail.CreateContainer(ctx, userID, api.MsgFolderRoot, mailFolderName)
			return created, clues.Wrap(err, "creating seed mail folder").OrNil()
		})
	if err != nil {
		return nil, summary, err
	}

	summary.record(folderCreated)

	folderID := ptr.Val(folder.GetId())

	// MailFolder has no createdDateTime/lastModifiedDateTime in Graph at
	// all (its model embeds Entity, not OutlookItem - see
	// manifest.go's doc comment on ManifestItem.CreatedAt for the other
	// container kinds this is also true for), so the folder's manifest
	// item below is correctly dateless; there is nothing to read.
	existingMsgs, err := listMessagesBySubject(ctx, mail, userID, folderID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed mail folder contents")
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadExchangeMail,
			Kind:       "folder",
			ID:         folderID,
			Name:       mailFolderName,
			ResourceID: userID,
		},
	}

	for _, want := range mailMessages {
		msg, msgCreated, err := FindOrCreate(
			func() (models.Messageable, bool, error) {
				m, found := existingMsgs[want.subject]
				return m, found, nil
			},
			func() (models.Messageable, error) {
				newMsg := models.NewMessage()
				newMsg.SetSubject(ptr.To(want.subject))

				body := models.NewItemBody()
				body.SetContent(ptr.To(want.body))
				body.SetContentType(ptr.To(models.TEXT_BODYTYPE))
				newMsg.SetBody(body)

				posted, err := mail.PostItem(ctx, userID, folderID, newMsg)
				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed message %q", want.subject))
				}

				return posted, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(msgCreated)

		items = append(items, datedManifestItem(
			msg,
			WorkloadExchangeMail,
			"message",
			want.subject,
			mailFolderName,
			int64(len(want.body)),
			userID))
	}

	return items, summary, nil
}

// messagesBySubject builds a subject->message map from an already-fetched
// slice of messages. Split out from listMessagesBySubject so the
// map-building logic - the actual idempotency decision input - is
// unit-testable without a Graph call.
func messagesBySubject(msgs []models.Messageable) map[string]models.Messageable {
	out := map[string]models.Messageable{}

	for _, msg := range msgs {
		out[ptr.Val(msg.GetSubject())] = msg
	}

	return out
}

// listMessagesBySubject returns a subject->message map of the messages
// already in folderID, so seedMail can decide per-message whether to
// create it, and - whether found or freshly created - read the same real
// Graph dates either way.
//
// api.Mail does not wrap "list messages in a folder" (only GetItem-by-ID and
// PostItem), so this reaches through to the raw Graph client the same way
// PostItemInContainer and PostDrive do elsewhere in this client package -
// Mail embeds api.Client, whose Stable field is exported for exactly this
// escape hatch.
func listMessagesBySubject(
	ctx context.Context,
	mail api.Mail,
	userID, folderID string,
) (map[string]models.Messageable, error) {
	resp, err := mail.Stable.
		Client().
		Users().
		ByUserId(userID).
		MailFolders().
		ByMailFolderId(folderID).
		Messages().
		Get(ctx, &users.ItemMailFoldersItemMessagesRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemMailFoldersItemMessagesRequestBuilderGetQueryParameters{
				Select: []string{"id", "subject", "createdDateTime", "lastModifiedDateTime"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing messages")
	}

	return messagesBySubject(resp.GetValue()), nil
}
