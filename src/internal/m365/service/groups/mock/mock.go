package stub

import (
	"github.com/twyouens/corso/src/pkg/backup/details"
)

func ItemInfo() details.ItemInfo {
	return details.ItemInfo{
		Groups: &details.GroupsInfo{
			ItemType: details.GroupsChannelMessage,
			ItemName: "itemID",
			Size:     1,
		},
	}
}
