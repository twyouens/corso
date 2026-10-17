package stub

import (
	"github.com/twyouens/corso/src/pkg/backup/details"
)

func ItemInfo() details.ItemInfo {
	return details.ItemInfo{
		TeamsChats: &details.TeamsChatsInfo{
			ItemType: details.TeamsChat,
			Chat:     details.ChatInfo{},
		},
	}
}
