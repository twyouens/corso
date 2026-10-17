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
// messages exist in userID's mailbox, and returns a manifest item for each.
func seedMail(ctx context.Context, c api.Client, userID string) ([]ManifestItem, error) {
	mail := c.Mail()

	folder, _, err := FindOrCreate(
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
		return nil, err
	}

	folderID := ptr.Val(folder.GetId())

	existingMsgs, err := listMessageIDsBySubject(ctx, mail, userID, folderID)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed mail folder contents")
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
		msgID, _, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existingMsgs[want.subject]
				return id, found, nil
			},
			func() (string, error) {
				msg := models.NewMessage()
				msg.SetSubject(ptr.To(want.subject))

				body := models.NewItemBody()
				body.SetContent(ptr.To(want.body))
				body.SetContentType(ptr.To(models.TEXT_BODYTYPE))
				msg.SetBody(body)

				posted, err := mail.PostItem(ctx, userID, folderID, msg)
				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed message %q", want.subject))
				}

				return ptr.Val(posted.GetId()), nil
			})
		if err != nil {
			return nil, err
		}

		items = append(items, ManifestItem{
			Workload:   WorkloadExchangeMail,
			Kind:       "message",
			ID:         msgID,
			Name:       want.subject,
			FolderPath: mailFolderName,
			SizeBytes:  int64(len(want.body)),
			ResourceID: userID,
		})
	}

	return items, nil
}

// listMessageIDsBySubject returns a subject->id map of the messages already
// in folderID, so SeedMail can decide per-message whether to create it.
//
// api.Mail does not wrap "list messages in a folder" (only GetItem-by-ID and
// PostItem), so this reaches through to the raw Graph client the same way
// PostItemInContainer and PostDrive do elsewhere in this client package -
// Mail embeds api.Client, whose Stable field is exported for exactly this
// escape hatch.
func listMessageIDsBySubject(
	ctx context.Context,
	mail api.Mail,
	userID, folderID string,
) (map[string]string, error) {
	resp, err := mail.Stable.
		Client().
		Users().
		ByUserId(userID).
		MailFolders().
		ByMailFolderId(folderID).
		Messages().
		Get(ctx, &users.ItemMailFoldersItemMessagesRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemMailFoldersItemMessagesRequestBuilderGetQueryParameters{
				Select: []string{"id", "subject"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing messages")
	}

	out := map[string]string{}

	for _, msg := range resp.GetValue() {
		out[ptr.Val(msg.GetSubject())] = ptr.Val(msg.GetId())
	}

	return out, nil
}
