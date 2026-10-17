package seed

import (
	"time"

	"github.com/twyouens/corso/src/internal/common/ptr"
)

// datedItem is the shape every Graph object this package builds a dated
// ManifestItem from actually has: messages, events and contacts (via
// OutlookItem), and drive items, drives, lists and list items (via
// BaseItem) all expose exactly these three getters. Defining the
// constraint once, rather than repeating "msg.GetId(),
// msg.GetCreatedDateTime(), msg.GetLastModifiedDateTime()" at every call
// site, is what makes datedManifestItem below a single, independently
// testable choke point for every workload's found-versus-created date
// handling, instead of five separate copies of the same three lines that
// could individually drift.
type datedItem interface {
	GetId() *string
	GetCreatedDateTime() *time.Time
	GetLastModifiedDateTime() *time.Time
}

// datedManifestItem builds a ManifestItem from any datedItem, reading
// ID/CreatedAt/ModifiedAt directly off the object. It does not care, and
// cannot tell, whether item came from a FindOrCreate find() or create()
// branch - which is exactly the property under test in
// dated_item_test.go, and exactly what regressed once already (see
// calendar.go's history: an earlier version special-cased the created
// branch to stamp time.Now() instead of reading the object, leaving a
// found item dateless). Every seed*() function that builds a dated item
// goes through this one function so that mistake has nowhere left to hide.
func datedManifestItem[T datedItem](
	item T,
	workload Workload,
	kind, name, folderPath string,
	sizeBytes int64,
	resourceID string,
) ManifestItem {
	return ManifestItem{
		Workload:   workload,
		Kind:       kind,
		ID:         ptr.Val(item.GetId()),
		Name:       name,
		FolderPath: folderPath,
		SizeBytes:  sizeBytes,
		CreatedAt:  item.GetCreatedDateTime(),
		ModifiedAt: item.GetLastModifiedDateTime(),
		ResourceID: resourceID,
	}
}
