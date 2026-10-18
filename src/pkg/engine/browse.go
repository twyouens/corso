package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twyouens/corso/src/pkg/backup/details"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/repository"
)

// Browse: listing what a backup point contains (RES-1, RES-2).
//
// Nothing in this file reads, fetches or returns item content, and
// nothing in it writes anything anywhere: ListFolders, ListItems and
// FindItemsByName are the complete read-only surface an admin needs to
// pick an object, pick a point in time, walk its folder tree and find an
// item by name. Restoring, exporting and erasing those items are
// deliberately not here.
//
// On names. RES-1 and RES-2 are about names, so Folder.Name,
// Folder.Path, Item.Name and Item.FolderPath all carry real Microsoft 365
// display names, subjects and filenames. That is data the caller asked
// for. It is also the one thing this package will not put anywhere else:
// no error returned from this file, and no log line it causes, ever
// contains an item name, subject, filename or folder name. Errors here
// name a backup point by its ID and an entry by its opaque, hashed entry
// ID (see Item.ID) - never by what it is called. A caller logging an
// error from this package can do so safely; a caller logging a Folder or
// an Item is logging backup content and must treat it accordingly.

// ErrBackupPointNotFound is returned by ListFolders and ListItems when no
// backup point in this repository has the requested ID. It wraps the
// fork's own pkg/repository.ErrorBackupNotFound, so errors.Is also
// matches that, but callers outside this module can only name (and so
// should only depend on) this exported error.
var ErrBackupPointNotFound = errors.New("engine: no backup point exists with that id")

// ErrIncompleteBackupPoint is returned by ListFolders and ListItems when
// the requested backup point is not independently restorable - an assist,
// preview or unclassified point (see BackupPointType) - and the request
// did not set AllowIncompletePoint.
//
// This is the deliberate guard against the mistake browse exists to make
// easy: ListBackupPoints returns every point in the repository, assist
// and preview points included (0.6 made that so on purpose, because an
// assist point left behind by one throttled item is real, tagged,
// storage-occupying content a tenant must be able to find and delete).
// But an assist point holds only the items that one interrupted backup
// managed to upload. Its folder tree is a *fragment* of the object, with
// no marker inside the data saying so. Rendered in a browse UI next to a
// merge point's tree, it looks exactly like a complete point in time -
// and an admin picking "restore this folder" from it would silently
// restore a subset. So browsing one is refused by default, and the caller
// has to say AllowIncompletePoint to get it.
//
// AllowIncompletePoint exists, rather than browse simply refusing these
// points outright, because erasure needs it: GDPR-2 requires erased items
// to disappear from every affected backup point, which means a future
// erasure chunk has to be able to enumerate the items inside an assist
// point in order to name them for removal. A flat refusal here would have
// re-created 0.6's own HIGH finding (content that exists but cannot be
// reached) one layer up. When it is set, FolderTree.IndependentlyRestorable
// and ItemList.IndependentlyRestorable report false and PointType says
// which kind of point it is, so a caller that opted in still cannot
// mistake the result for a complete tree.
var ErrIncompleteBackupPoint = errors.New("engine: backup point is not independently restorable")

// ErrFolderNotFound is returned by ListItems when FolderID names no
// folder in the requested backup point. A caller gets folder IDs from
// ListFolders on the same backup point; an ID from a different point is
// not valid here even if the two points cover the same object, because an
// ID is only meaningful within the point it was listed from (see
// Folder.ID).
var ErrFolderNotFound = errors.New("engine: no folder with that id in that backup point")

// ErrMissingBackupPointID is returned by ListFolders and ListItems when
// the request carries no backup point ID. An empty ID never means "any
// point" or "the latest point": a caller picks a point explicitly with
// ListBackupPoints first, which is also the only way to know whether the
// point it picked is independently restorable.
var ErrMissingBackupPointID = errors.New("engine: a backup point id is required")

// ErrEmptyNameQuery is returned by FindItemsByName when Name is empty.
// An empty query never means "every item": a caller that wants every
// item in a point calls ListItems with no FolderID, so that intent is
// visible at the call site. (Same reasoning as ErrEmptyTagFilter.)
var ErrEmptyNameQuery = errors.New("engine: FindItemsByName requires a non-empty name")

// NameMatch selects how FindItemsByName compares its Name query against
// an item's name. Both modes compare *names only* - never message
// bodies, file contents, attachments or any other item content. RES-2
// rules full-text content search out of scope, and nothing in this
// package reads an item's content at all: matching runs entirely against
// the names already recorded in the backup point's own details manifest,
// so a find never touches the stored data and never decrypts anything.
type NameMatch string

const (
	// NameMatchContains matches an item whose name contains the query as
	// a substring. This is NameMatch's zero value, so a caller that
	// leaves FindItemsRequest.Match unset gets substring matching -
	// chosen as the default because an admin searching for an item by
	// name rarely knows its exact full subject or filename.
	NameMatchContains NameMatch = ""
	// NameMatchExact matches an item whose whole name equals the query.
	NameMatchExact NameMatch = "exact"
)

// Folder is one container inside a backup point: a mail folder, a
// calendar, a contacts folder, a OneDrive folder, a folder inside a
// SharePoint document library, or a SharePoint list's own container
// (RES-1). Every field is a plain Go type.
type Folder struct {
	// ID identifies this folder within this backup point, stably: the
	// same folder in the same backup point is the same ID on every call,
	// because it is derived (by the fork, when the backup was written)
	// from the folder's immutable storage path and stored in the point's
	// own details manifest - not generated per call and not dependent on
	// listing order. It is an opaque, hashed, non-reversible value: safe
	// to log, store and put in a URL, and it reveals no folder name.
	//
	// Not an ID a caller should reuse across backup points: the same
	// mail folder in two different points may well have the same ID
	// (the storage path it hashes is the same), but nothing guarantees
	// that, and ListItems rejects an ID that is not in the point being
	// listed (ErrFolderNotFound).
	ID string

	// ParentID is the ID of the Folder this one sits inside, or "" when
	// this folder is at the top of what the backup point recorded. Use
	// this, not Path, to reconstruct the tree: Path is a display string
	// and is ambiguous if any folder name contains a "/".
	ParentID string

	// Name is the folder's Microsoft 365 display name, exactly as the
	// backup recorded it. Real content: see this file's note on names.
	//
	// One documented exception, confirmed against the real test tenant
	// rather than assumed: for a SharePoint list's container (Category
	// "lists") the backup records no display name, only the list's own
	// Microsoft 365 list ID, so that ID is what Name and Path carry. The
	// list's human title is on the Item inside it - there is exactly one,
	// the list itself. A caller rendering a tree should label a "lists"
	// container from the item inside it, not from this field.
	Name string

	// Path is Name prefixed by every ancestor folder's display name,
	// joined with "/" - for display to an admin. Display names are not
	// escaped, so a folder whose own name contains "/" produces an
	// ambiguous Path; ParentID is the unambiguous structure.
	//
	// For OneDrive and SharePoint library folders the first element is
	// the drive's own root ("root:"), because that is genuinely how the
	// backup records a drive's folder tree. DriveName below is the
	// human-readable name of the drive (for SharePoint, the document
	// library) that root belongs to.
	Path string

	// Service is the Microsoft 365 service this folder belongs to, as the
	// fork's own stable wire name: "exchange", "onedrive", "sharepoint",
	// "groups" or "teamsChats".
	Service string

	// Category is the data category within Service, as the fork's own
	// stable wire name: "email", "contacts", "events", "files",
	// "libraries", "lists" or "pages". This is what tells a mail folder
	// from a calendar from a contacts folder, all three of which can be
	// top-level folders in one Exchange backup point with nothing in
	// their names to distinguish them.
	Category string

	// DriveID and DriveName identify the OneDrive drive or SharePoint
	// document library this folder lives in. Both are empty for Exchange
	// folders, which are not drive-backed. DriveName is the library's
	// display name, which for SharePoint is how an admin recognises which
	// document library they are browsing - the library itself is a drive,
	// not a Folder entry of its own.
	DriveID   string
	DriveName string

	// SizeBytes is the total size of every item at or below this folder,
	// as the backup point itself recorded it - a roll-up, not the size of
	// this folder's direct children only. Zero for a folder whose items
	// carry no size (Exchange calendars and contacts folders routinely
	// do).
	SizeBytes int64

	// ModifiedAt is the most recent modification time of any item at or
	// below this folder, as the backup point recorded it, or the zero
	// time if none of them carried one.
	ModifiedAt time.Time
}

// Item is one backed-up object inside a Folder: a mail message, a
// calendar event, a contact, a file in OneDrive or a SharePoint document
// library, or a SharePoint list. Every field is a plain Go type.
type Item struct {
	// ID identifies this item within this backup point, stably: the same
	// item in the same backup point is the same ID on every call. It is
	// the value the fork's own item selectors accept as an item
	// reference, so it is the handle a later restore (5.1) or erasure
	// (7.3) takes as input - which is exactly why it must not be
	// generated per call. It is derived (by the fork, when the backup was
	// written) from the item's immutable storage path and stored in the
	// point's own details manifest.
	//
	// Opaque, hashed and non-reversible: safe to log, store and put in a
	// URL, and it reveals no item name, subject or filename. It is not
	// the item's Microsoft 365 ID and is not meaningful outside this
	// repository.
	ID string

	// FolderID is the ID of the Folder this item sits in, or "" for an
	// item the backup point recorded outside any folder. Every item every
	// v1 workload produces does sit in a folder - including a SharePoint
	// list, whose container is named by its list ID (see Folder.Name) -
	// so "" here means the backup recorded no location for this item, not
	// that it belongs at the top of the tree. ListFolders on the same
	// backup point returns the matching Folder.
	FolderID string

	// FolderPath is that folder's Path - repeated here so a caller
	// rendering search results does not have to join them against a
	// folder listing. Empty when FolderID is empty. Real content: see
	// this file's note on names.
	FolderPath string

	// Name is the item's human name as the backup recorded it: a mail
	// message's or calendar event's subject, a contact's display name, a
	// file's filename, a list's title. Empty if the backup point recorded
	// none. Real content: see this file's note on names.
	Name string

	// SizeBytes is the item's size in bytes as the backup recorded it, or
	// 0 for an item kind that carries no size. Exchange calendar events
	// and contacts commonly report 0; it means "not recorded", not
	// "empty".
	SizeBytes int64

	// CreatedAt and ModifiedAt are the item's own Microsoft 365
	// creation and last-modified times as the backup recorded them, not
	// the time it was backed up (BackupPoint.CreatedAt is that). Either
	// is the zero time when the backup point recorded none - which is
	// normal, not an error: Microsoft Graph does not return a date for
	// every kind of object (see 0.3's seed manifest, where mail folders,
	// calendars and contacts folders are genuinely dateless).
	CreatedAt  time.Time
	ModifiedAt time.Time

	// ItemCount is how many child objects this item itself contains, and
	// is non-zero only for a SharePoint list (Category "lists").
	//
	// It exists because the fork records a whole list as a *single*
	// backed-up item - one entry carrying the list's name, dates and the
	// number of list items inside it - rather than one entry per list
	// item. So a list's own rows are not individually browsable, and not
	// individually addressable by Item.ID either: the list is the unit
	// that can be listed here (and, when 5.1 and 7.3 arrive, the unit
	// they will be handed). This count is the only thing browse can say
	// about what is inside one, and it is what an admin needs in order to
	// recognise the list they are looking at. Confirmed live: a list
	// seeded with two rows reports ItemCount 2 and lists as one item.
	// Zero for every other kind of item, where it means nothing rather
	// than "empty".
	ItemCount int64

	// Service and Category are as described on Folder.
	Service  string
	Category string

	// DriveID and DriveName are as described on Folder: set for OneDrive
	// and SharePoint library items, empty for Exchange items.
	DriveID   string
	DriveName string
}

// FolderTreeRequest asks for one backup point's folder tree.
type FolderTreeRequest struct {
	// BackupPointID is the BackupPoint.ID to browse, from
	// ListBackupPoints. Required: see ErrMissingBackupPointID.
	BackupPointID string

	// AllowIncompletePoint permits browsing a backup point that is not
	// independently restorable. Leave it false for anything an admin will
	// restore from; see ErrIncompleteBackupPoint for the full reasoning
	// and for why this escape hatch exists at all.
	AllowIncompletePoint bool
}

// FolderTree is one backup point's complete folder tree, flattened.
type FolderTree struct {
	// BackupPointID is the point this tree came from.
	BackupPointID string

	// PointType is that point's type, so a caller that set
	// AllowIncompletePoint can see what it actually got.
	PointType BackupPointType

	// IndependentlyRestorable is PointType.IsIndependentlyRestorable().
	// When false, Folders is a *fragment* of the object - whatever one
	// interrupted or deliberately partial backup managed to record - and
	// must not be presented as a complete point in time.
	IndependentlyRestorable bool

	// Folders is every folder the backup point recorded, flattened into
	// one slice rather than nested: reconstruct the hierarchy with
	// Folder.ParentID, whose "" value marks a top-level folder. Flat
	// because a backup point mixes several services and categories, so
	// there is no single root to nest under, and because a caller
	// rendering a tree lazily wants to index by ID anyway.
	//
	// Deterministically ordered by (Path, ID), on every call. The
	// underlying details manifest stores folder entries in a
	// map-iteration order that genuinely varies between calls, so this
	// ordering is applied here rather than inherited.
	Folders []Folder
}

// ItemsRequest asks for the items in one folder, or in one whole backup
// point.
type ItemsRequest struct {
	// BackupPointID is the BackupPoint.ID to browse, from
	// ListBackupPoints. Required: see ErrMissingBackupPointID.
	BackupPointID string

	// FolderID limits the result to the items directly inside that one
	// folder - not its subfolders' items. Leave it empty to get every
	// item in the whole backup point, across every service and category
	// it covers. A FolderID that is not in this backup point is
	// ErrFolderNotFound, never silently empty.
	FolderID string

	// AllowIncompletePoint is as described on FolderTreeRequest.
	AllowIncompletePoint bool
}

// ItemList is the items one ItemsRequest matched.
type ItemList struct {
	// BackupPointID, PointType and IndependentlyRestorable are as
	// described on FolderTree.
	BackupPointID           string
	PointType               BackupPointType
	IndependentlyRestorable bool

	// Items is the matching items, deterministically ordered by
	// (FolderPath, Name, ID) on every call.
	Items []Item
}

// FindItemsRequest asks for the items matching a name across one object's
// backup points (RES-2).
type FindItemsRequest struct {
	// Tags scopes the search to the backup points carrying all of these
	// key/value pairs - for k3ep, its own "k3ep-object" (and
	// "k3ep-tenant") tag, which is what makes this "within one object's
	// backup points" rather than a repository-wide search. Required and
	// non-empty: see ErrEmptyTagFilter. Matching is exactly
	// ListBackupPoints' matching.
	Tags map[string]string

	// Name is the name to look for. Required: see ErrEmptyNameQuery.
	// Compared case-insensitively (by simple Unicode lower-casing, not
	// locale-aware collation) against item names only - never content.
	Name string

	// Match selects substring or whole-name comparison. Its zero value
	// is NameMatchContains.
	Match NameMatch

	// AllowIncompletePoints includes backup points that are not
	// independently restorable in the search. Default false, which
	// searches only merge points; see ErrIncompleteBackupPoint for why
	// this is opt-in. Unlike ListFolders and ListItems, this never
	// returns ErrIncompleteBackupPoint - a find spans many points, so a
	// non-restorable one is skipped rather than failing the whole search.
	// Each result says which point it came from and what type that point
	// is.
	AllowIncompletePoints bool
}

// FoundItem is one item FindItemsByName matched, with the backup point it
// was found in.
type FoundItem struct {
	// Item is the matched item, exactly as ListItems would report it.
	Item Item

	// BackupPointID is the point this item was found in. Item.ID is only
	// meaningful together with it.
	BackupPointID string

	// BackupPointType and BackupPointIndependentlyRestorable describe
	// that point, so a caller that set AllowIncompletePoints can tell a
	// hit in a complete point from a hit in a fragment.
	BackupPointType                    BackupPointType
	BackupPointIndependentlyRestorable bool

	// BackupPointCreatedAt is when that backup point was created - the
	// "point in time" an admin chose in RES-1, as opposed to
	// Item.CreatedAt, which is when Microsoft 365 created the item.
	BackupPointCreatedAt time.Time
}

// ListFolders returns the folder tree a single backup point recorded:
// Exchange mail folders, calendars and contacts folders, OneDrive
// folders, and the folders inside SharePoint document libraries (RES-1).
// It reads only the point's own details manifest - the index the backup
// wrote describing what it captured - so it never fetches, decrypts or
// returns item content, and it makes no Microsoft Graph call at all: the
// object no longer has to exist in Microsoft 365 for its backup to be
// browsable.
//
// The tree is exactly what the backup point recorded, not a tidied
// version of it. In particular:
//
//   - A folder only appears if the backup point recorded at least one
//     item at or below it. The fork derives folder entries from item
//     entries when it writes a backup, so a folder that was empty at
//     backup time is not in the manifest and cannot be listed here.
//   - For OneDrive and SharePoint libraries the top of each drive's tree
//     is the drive's own root element ("root:"), with the drive named in
//     Folder.DriveName.
//   - A SharePoint document library is a drive, not a folder entry: it
//     is identified by the DriveID/DriveName on the folders and items
//     inside it, whose own top-level folder is the drive's "root:".
//   - A SharePoint list does get a container folder, but one named by
//     the list's own Microsoft 365 ID rather than its title (see
//     Folder.Name), holding exactly one item: the list itself, whose
//     Name is the title and whose ItemCount is how many rows it holds.
//
// Returns ErrIncompleteBackupPoint for a point that is not independently
// restorable unless the request opts in - read that error's doc comment
// before setting AllowIncompletePoint.
func (r *Repository) ListFolders(ctx context.Context, req FolderTreeRequest) (FolderTree, error) {
	dm, point, err := r.backupDetails(ctx, req.BackupPointID, req.AllowIncompletePoint)
	if err != nil {
		return FolderTree{}, err
	}

	folders, _, err := browseEntries(dm, point.ID)
	if err != nil {
		return FolderTree{}, err
	}

	return FolderTree{
		BackupPointID:           point.ID,
		PointType:               point.Type,
		IndependentlyRestorable: point.Type.IsIndependentlyRestorable(),
		Folders:                 folders,
	}, nil
}

// ListItems returns the items one backup point recorded, each with a
// stable ID, a name, a size and its own creation and modification dates
// (RES-1). With req.FolderID set it returns the items directly inside
// that folder; with it empty, every item in the point.
//
// Like ListFolders it reads only the point's details manifest: no item
// content is fetched or decrypted, and no Graph call is made. Metadata
// entries the fork stores alongside real items (OneDrive permission
// sidecars) are never returned - they are not items an admin backed up.
//
// Returns ErrIncompleteBackupPoint for a point that is not independently
// restorable unless the request opts in, and ErrFolderNotFound for a
// FolderID the point does not contain.
func (r *Repository) ListItems(ctx context.Context, req ItemsRequest) (ItemList, error) {
	dm, point, err := r.backupDetails(ctx, req.BackupPointID, req.AllowIncompletePoint)
	if err != nil {
		return ItemList{}, err
	}

	folders, items, err := browseEntries(dm, point.ID)
	if err != nil {
		return ItemList{}, err
	}

	if len(req.FolderID) > 0 {
		if !folderExists(folders, req.FolderID) {
			return ItemList{}, fmt.Errorf("%w: %s in backup point %s",
				ErrFolderNotFound, req.FolderID, point.ID)
		}

		kept := make([]Item, 0, len(items))

		for _, it := range items {
			if it.FolderID == req.FolderID {
				kept = append(kept, it)
			}
		}

		items = kept
	}

	return ItemList{
		BackupPointID:           point.ID,
		PointType:               point.Type,
		IndependentlyRestorable: point.Type.IsIndependentlyRestorable(),
		Items:                   items,
	}, nil
}

// FindItemsByName returns every item whose name matches req.Name, across
// every backup point carrying req.Tags (RES-2). It is name matching only:
// full-text search of message bodies or file contents is explicitly out
// of scope for v1, and this function could not do it anyway - it reads
// each point's details manifest and never opens stored item data.
//
// req.Tags is how "within one object's backup points" is expressed, and
// is required (ErrEmptyTagFilter): k3ep passes its own object tag. The
// search covers only independently-restorable points unless
// AllowIncompletePoints is set; points it skips for that reason are
// skipped silently, since a find spans many points and one fragment
// among them is not a reason to fail the search.
//
// Cost: this reads one details manifest per matching backup point, so it
// is linear in the number of points the tags select, not in the size of
// the repository. Scope the tags to one object.
//
// Results are ordered newest backup point first (by that point's own
// creation time, then its ID to break ties), and within a point by
// (FolderPath, Name, ID) - deterministic on every call.
func (r *Repository) FindItemsByName(ctx context.Context, req FindItemsRequest) ([]FoundItem, error) {
	if len(req.Tags) == 0 {
		return nil, ErrEmptyTagFilter
	}

	if len(req.Name) == 0 {
		return nil, ErrEmptyNameQuery
	}

	points, err := r.ListBackupPoints(ctx, req.Tags)
	if err != nil {
		return nil, err
	}

	// Newest point first, ID to break ties, so the order a caller sees
	// does not depend on the order the model store happened to return.
	sort.SliceStable(points, func(i, j int) bool {
		if !points[i].CreatedAt.Equal(points[j].CreatedAt) {
			return points[i].CreatedAt.After(points[j].CreatedAt)
		}

		return points[i].ID < points[j].ID
	})

	found := []FoundItem{}

	for _, point := range points {
		if !req.AllowIncompletePoints && !point.Type.IsIndependentlyRestorable() {
			continue
		}

		// AllowIncompletePoint is set unconditionally here because the
		// skip above has already applied the caller's own policy; without
		// it, an opted-in search would fail on the first assist point.
		dm, _, err := r.backupDetails(ctx, point.ID, true)
		if err != nil {
			return nil, err
		}

		_, items, err := browseEntries(dm, point.ID)
		if err != nil {
			return nil, err
		}

		for _, it := range items {
			if !nameMatches(it.Name, req.Name, req.Match) {
				continue
			}

			found = append(found, FoundItem{
				Item:                               it,
				BackupPointID:                      point.ID,
				BackupPointType:                    point.Type,
				BackupPointIndependentlyRestorable: point.Type.IsIndependentlyRestorable(),
				BackupPointCreatedAt:               point.CreatedAt,
			})
		}
	}

	return found, nil
}

// nameMatches implements NameMatch. Case-insensitive in both modes, by
// simple Unicode lower-casing: good enough for an admin typing part of a
// filename, and deliberately not locale-aware collation, which would
// make the same query return different results on different hosts. An
// item with no recorded name never matches a non-empty query, including
// under NameMatchContains, where the empty string is technically a
// substring of nothing.
func nameMatches(name, query string, mode NameMatch) bool {
	if len(name) == 0 {
		return false
	}

	name = strings.ToLower(name)
	query = strings.ToLower(query)

	if mode == NameMatchExact {
		return name == query
	}

	return strings.Contains(name, query)
}

func folderExists(folders []Folder, id string) bool {
	for _, f := range folders {
		if f.ID == id {
			return true
		}
	}

	return false
}

// backupDetails loads one backup point's details manifest and classifies
// the point, applying the ErrIncompleteBackupPoint policy. It filters out
// the fork's OneDrive metadata sidecar entries up front (via the details
// model's own FilterMetaFiles, since the per-entry predicate it uses is
// unexported) so no caller of this helper has to remember to.
func (r *Repository) backupDetails(
	ctx context.Context,
	backupPointID string,
	allowIncomplete bool,
) (details.DetailsModel, BackupPoint, error) {
	if len(backupPointID) == 0 {
		return details.DetailsModel{}, BackupPoint{}, ErrMissingBackupPointID
	}

	// GetBackupDetails reports its failure through the returned fault
	// bus rather than an error return; errs.Failure() is the fork's own
	// way of asking "did this fail outright", and is what the CLI's
	// genericDetailsCore checks.
	deets, b, errs := r.repo.GetBackupDetails(ctx, backupPointID)

	if errs != nil {
		if err := errs.Failure(); err != nil {
			if errors.Is(err, repository.ErrorBackupNotFound) {
				return details.DetailsModel{}, BackupPoint{},
					fmt.Errorf("%w: %s: %w", ErrBackupPointNotFound, backupPointID, err)
			}

			return details.DetailsModel{}, BackupPoint{},
				fmt.Errorf("engine: reading backup point %s details: %w", backupPointID, err)
		}
	}

	if b == nil {
		return details.DetailsModel{}, BackupPoint{},
			fmt.Errorf("%w: %s", ErrBackupPointNotFound, backupPointID)
	}

	point := toBackupPoint(b)

	if !allowIncomplete && !point.Type.IsIndependentlyRestorable() {
		return details.DetailsModel{}, BackupPoint{},
			fmt.Errorf("%w: %s is of type %s", ErrIncompleteBackupPoint, point.ID, point.Type)
	}

	if deets == nil {
		return details.DetailsModel{}, BackupPoint{},
			fmt.Errorf("engine: backup point %s has no details manifest", point.ID)
	}

	return deets.FilterMetaFiles(), point, nil
}

// locationSeparator joins the parts of a folder's identity into a map
// key. A NUL byte cannot appear in a Microsoft 365 display name, a
// service or category name or a drive ID, so using it as the separator
// means two different folders can never collide on the same key the way
// a "/" separator would for a folder whose own name contains a "/".
const locationSeparator = "\x00"

// folderKey builds the key that identifies one folder within one backup
// point, for matching a child folder and an item to their parent.
//
// The location (the logical path of display names) is not enough on its
// own, and this is not a theoretical concern: every drive's folder tree
// starts at an element literally named "root:", so a user with two
// OneDrive drives, or a site with two document libraries, or - as a
// single backup point covering OneDrive and SharePoint libraries will
// always have - one of each, produces several distinct folders whose
// location is exactly "root:". Keying on location alone silently merged
// them, which attached a folder to the wrong parent. Caught by
// TestListFolders_TreeCoversEveryWorkload.
//
// The fork has the same problem internally and solves it the same way:
// its own "unique location" prefixes the location with the category and,
// for drive-backed data, the drive ID (see details.NewOneDriveLocationIDer
// and friends) - and then strips that prefix back off before storing the
// location in the manifest, which is why it has to be reconstructed here.
func folderKey(service, category, driveID string, locationElements []string) string {
	parts := make([]string, 0, len(locationElements)+3)
	parts = append(parts, service, category, driveID)
	parts = append(parts, locationElements...)

	return strings.Join(parts, locationSeparator)
}

// browseEntries converts one backup point's details manifest into this
// package's own Folder and Item values.
//
// The hierarchy comes from each entry's location - the logical path of
// display names the fork records alongside the storage path - rather than
// from the manifest's own parent-reference field, which the fork writes
// but has never read anywhere and which is a hash of the *storage* path.
// Location-based keying is what the fork's own test helpers use to
// reconstruct a tree from a details manifest, and it keeps a folder and
// the items inside it keyed identically by construction.
//
// Both results are sorted deterministically. That is not cosmetic: the
// fork emits folder entries from a Go map, so their order genuinely
// varies between two reads of the same manifest.
//
// backupPointID is used only to identify the point in an error. Errors
// from here name the point by ID and the offending entry by its own
// opaque entry ID, never by any folder or item name.
func browseEntries(dm details.DetailsModel, backupPointID string) ([]Folder, []Item, error) {
	type folderRecord struct {
		folder    Folder
		parentKey string
	}

	var (
		// byKey maps a folder's full location key to its record, so an
		// item (and a child folder) can find its parent.
		byKey   = map[string]*folderRecord{}
		records = make([]*folderRecord, 0, len(dm.Entries))
		items   = make([]Item, 0, len(dm.Entries))
	)

	for _, ent := range dm.Entries {
		if ent.Folder == nil {
			continue
		}

		// A folder entry's own location field holds its *parent's*
		// location; its own display name is in the folder info. That is
		// how the fork writes them (see details.Builder.addFolderEntries),
		// and the fork's own test helpers reconstruct the full path the
		// same way.
		parentElems, err := locationElements(ent.LocationRef)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"engine: parsing folder location in backup point %s, entry %s: %w",
				backupPointID, ent.ShortRef, err)
		}

		fullElems := append(append([]string{}, parentElems...), ent.Folder.DisplayName)

		svc, cat, err := serviceAndCategory(ent.RepoRef, false)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"engine: parsing folder path in backup point %s, entry %s: %w",
				backupPointID, ent.ShortRef, err)
		}

		rec := &folderRecord{
			folder: Folder{
				ID:         ent.ShortRef,
				Name:       ent.Folder.DisplayName,
				Path:       strings.Join(fullElems, "/"),
				Service:    svc,
				Category:   cat,
				DriveID:    ent.Folder.DriveID,
				DriveName:  ent.Folder.DriveName,
				SizeBytes:  ent.Folder.Size,
				ModifiedAt: ent.Folder.Modified,
			},
			parentKey: folderKey(svc, cat, ent.Folder.DriveID, parentElems),
		}

		records = append(records, rec)
		byKey[folderKey(svc, cat, ent.Folder.DriveID, fullElems)] = rec
	}

	// Wire parents only once every folder is known: the manifest's own
	// entry order does not put parents before children.
	for _, rec := range records {
		if parent, ok := byKey[rec.parentKey]; ok {
			rec.folder.ParentID = parent.folder.ID
		}
	}

	for _, ent := range dm.Entries {
		if ent.Folder != nil {
			continue
		}

		locElems, err := locationElements(ent.LocationRef)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"engine: parsing item location in backup point %s, entry %s: %w",
				backupPointID, ent.ShortRef, err)
		}

		svc, cat, err := serviceAndCategory(ent.RepoRef, true)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"engine: parsing item path in backup point %s, entry %s: %w",
				backupPointID, ent.ShortRef, err)
		}

		driveID, driveName := itemDrive(ent.ItemInfo)

		it := Item{
			ID:         ent.ShortRef,
			Name:       itemName(ent.ItemInfo),
			SizeBytes:  itemSize(ent.ItemInfo),
			ItemCount:  itemChildCount(ent.ItemInfo),
			CreatedAt:  itemCreated(ent.ItemInfo),
			ModifiedAt: ent.ItemInfo.Modified(),
			Service:    svc,
			Category:   cat,
			DriveID:    driveID,
			DriveName:  driveName,
		}

		if folder, ok := byKey[folderKey(svc, cat, driveID, locElems)]; ok {
			it.FolderID = folder.folder.ID
			it.FolderPath = folder.folder.Path
		}

		items = append(items, it)
	}

	folders := make([]Folder, 0, len(records))
	for _, rec := range records {
		folders = append(folders, rec.folder)
	}

	sort.SliceStable(folders, func(i, j int) bool {
		if folders[i].Path != folders[j].Path {
			return folders[i].Path < folders[j].Path
		}

		return folders[i].ID < folders[j].ID
	})

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].FolderPath != items[j].FolderPath {
			return items[i].FolderPath < items[j].FolderPath
		}

		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}

		return items[i].ID < items[j].ID
	})

	return folders, items, nil
}

// locationElements splits a details manifest location field into its
// unescaped display-name elements. An empty location is the root, not an
// error: an item or folder at the top of what the point recorded has no
// ancestors to name.
func locationElements(locationRef string) ([]string, error) {
	if len(locationRef) == 0 {
		return nil, nil
	}

	pb, err := path.Builder{}.SplitUnescapeAppend(locationRef)
	if err != nil {
		return nil, err
	}

	return pb.Elements(), nil
}

// serviceAndCategory reads the service and category out of an entry's
// storage path, as the fork's own stable wire names.
//
// Needed because the location field alone cannot tell an Exchange mail
// folder from a calendar from a contacts folder: for Exchange the
// category is the location's hidden prefix element, so all three look
// like top-level folders with nothing but their display names to
// distinguish them. The storage path always carries it.
//
// SharePoint lists are recorded as prefix-length paths with no folder
// element, so a strict parse is tried first and the shorter prefix form
// second.
func serviceAndCategory(repoRef string, isItem bool) (string, string, error) {
	p, err := path.FromDataLayerPath(repoRef, isItem)
	if err != nil {
		p, err = path.PrefixOrPathFromDataLayerPath(repoRef, isItem)
		if err != nil {
			return "", "", err
		}
	}

	return p.Service().String(), p.Category().String(), nil
}

// itemName reads an item's human name from its service-specific info.
//
// The fork's details package has no exported accessor for this: its
// per-service info structs name the field differently (Subject for mail
// and events, ContactName for contacts, ItemName for drive items) and the
// method that dispatches on item type is unexported, so this switch is
// written here rather than reused. It returns "" rather than an error for
// an info shape it does not recognise: a missing name is a gap in what
// the backup recorded, not a failure to list.
func itemName(info details.ItemInfo) string {
	switch {
	case info.Exchange != nil:
		if info.Exchange.ItemType == details.ExchangeContact {
			return info.Exchange.ContactName
		}

		// Mail messages and calendar events both carry Subject.
		return info.Exchange.Subject

	case info.OneDrive != nil:
		return info.OneDrive.ItemName

	case info.SharePoint != nil:
		if len(info.SharePoint.ItemName) > 0 {
			return info.SharePoint.ItemName
		}

		// A list's own entry carries its title under List, not ItemName.
		if info.SharePoint.List != nil {
			return info.SharePoint.List.Name
		}

		return ""

	case info.Groups != nil:
		switch info.Groups.ItemType {
		case details.GroupsChannelMessage:
			return info.Groups.Message.Subject
		case details.GroupsConversationPost:
			return info.Groups.Post.Topic
		default:
			return info.Groups.ItemName
		}

	case info.TeamsChats != nil:
		return info.TeamsChats.Chat.Name
	}

	return ""
}

// itemSize reads an item's size in bytes from its service-specific info.
//
// Deliberately not the fork's own ItemInfo.size(), which is unexported
// and which, for a Teams chat, returns the chat's *message count* rather
// than a byte size. Item.SizeBytes promises bytes, so a Teams chat
// reports 0 here instead of a number that is not a size.
func itemSize(info details.ItemInfo) int64 {
	switch {
	case info.Exchange != nil:
		return info.Exchange.Size
	case info.OneDrive != nil:
		return info.OneDrive.Size
	case info.SharePoint != nil:
		return info.SharePoint.Size
	case info.Groups != nil:
		switch info.Groups.ItemType {
		case details.GroupsChannelMessage:
			return info.Groups.Message.Size
		case details.GroupsConversationPost:
			return info.Groups.Post.Size
		default:
			return info.Groups.Size
		}
	}

	return 0
}

// itemChildCount reads how many child objects an item itself contains.
// Only a SharePoint list has one: see Item.ItemCount.
func itemChildCount(info details.ItemInfo) int64 {
	if info.SharePoint != nil && info.SharePoint.List != nil {
		return info.SharePoint.List.ItemCount
	}

	return 0
}

// itemCreated reads an item's Microsoft 365 creation time from its
// service-specific info. The fork exports Modified() for the matching
// job but has no Created() counterpart, so this mirrors it.
func itemCreated(info details.ItemInfo) time.Time {
	switch {
	case info.Exchange != nil:
		return info.Exchange.Created
	case info.OneDrive != nil:
		return info.OneDrive.Created
	case info.SharePoint != nil:
		return info.SharePoint.Created
	case info.Groups != nil:
		switch info.Groups.ItemType {
		case details.GroupsChannelMessage:
			return info.Groups.Message.CreatedAt
		case details.GroupsConversationPost:
			return info.Groups.Post.CreatedAt
		default:
			return info.Groups.Created
		}
	case info.TeamsChats != nil:
		return info.TeamsChats.Chat.CreatedAt
	}

	return time.Time{}
}

// itemDrive reads the drive (for SharePoint, the document library) an
// item belongs to, or two empty strings for an item that is not
// drive-backed.
func itemDrive(info details.ItemInfo) (string, string) {
	switch {
	case info.OneDrive != nil:
		return info.OneDrive.DriveID, info.OneDrive.DriveName
	case info.SharePoint != nil:
		return info.SharePoint.DriveID, info.SharePoint.DriveName
	case info.Groups != nil:
		return info.Groups.DriveID, info.Groups.DriveName
	}

	return "", ""
}
