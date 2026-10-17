package seed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/alcionai/clues"
)

// ManifestSchemaVersion is bumped whenever the shape of Manifest or
// ManifestItem changes incompatibly. Consumers (0.7's library API tests,
// 3.5's k3ep tests) should check it before relying on new fields.
//
// Version history:
//   - 1: initial schema (chunk 0.3, first round). CreatedAt/ModifiedAt
//     existed as plain time.Time but were not actually populated by five
//     of six workloads, and disagreed between a found and a created item
//     for the sixth - see version 2.
//   - 2: CreatedAt/ModifiedAt changed from time.Time to *time.Time, and
//     are now genuinely populated, from the same real Graph
//     createdDateTime/lastModifiedDateTime, identically whether the item
//     was found already existing or just created. A consumer checking for
//     schema version 1 and reading these two fields as non-pointer
//     time.Time will fail to compile/unmarshal against a version-2
//     manifest; a consumer written against version 2 must treat a
//     missing/null CreatedAt or ModifiedAt as "Graph has no date for this
//     item's kind" (see the field's own doc comment for exactly which
//     three container kinds that applies to), not as an error.
//   - 3: added ManifestItem.Recurrence (nil except on the one recurring
//     calendar event, Kind "event_recurring") and a new Kind,
//     "event_exception", for that series' one exception occurrence as its
//     own top-level item. Purely additive - every field from version 2
//     keeps its meaning, and a consumer that ignores unknown JSON fields
//     and never asks for Kind "event_recurring"/"event_exception" is
//     unaffected - but bumped anyway per this constant's own rule, since a
//     consumer that *does* want to assert against a recurring series
//     needs to know this version is the first one where that's possible.
const ManifestSchemaVersion = 3

// Workload names the v1 workload a manifest item belongs to. These strings
// are the contract later chunks assert against; don't rename a value once
// anything depends on it (add a new one instead, and bump
// ManifestSchemaVersion if the meaning of an existing one changes).
type Workload string

const (
	WorkloadExchangeMail      Workload = "exchange-mail"
	WorkloadExchangeCalendar  Workload = "exchange-calendar"
	WorkloadExchangeContacts  Workload = "exchange-contacts"
	WorkloadOneDrive          Workload = "onedrive"
	WorkloadSharePointLibrary Workload = "sharepoint-library"
	WorkloadSharePointList    Workload = "sharepoint-list"
)

// ManifestItem describes exactly one thing the seeder created or found
// already present (because a previous run created it) in the test tenant.
// Every field a later chunk's assertion might need is here: a stable ID,
// a human name, where it lives, and when/how big it is.
type ManifestItem struct {
	// Workload is the v1 workload this item belongs to.
	Workload Workload `json:"workload"`

	// Kind distinguishes items within a workload, e.g. "folder" vs
	// "message", or "library" vs "list_item". Free text, but stable once
	// used: later chunks may filter on it.
	Kind string `json:"kind"`

	// ID is the item's Microsoft Graph ID. Stable across seeder runs: the
	// seeder looks up an existing item by name before creating a new one, so
	// re-running never changes a previously-recorded ID.
	ID string `json:"id"`

	// Name is the item's display name or subject - whatever Graph property
	// the seeder matched on to decide "this already exists".
	Name string `json:"name"`

	// FolderPath is the sequence of container display names from the root
	// of the owning mailbox/drive down to (and including) this item's
	// immediate parent, joined with "/". Empty for top-level containers.
	FolderPath string `json:"folder_path,omitempty"`

	// SizeBytes is the content size for items that have one (OneDrive
	// files, mail attachments). Zero/omitted for items with no size.
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// CreatedAt and ModifiedAt come straight from Graph's own
	// createdDateTime/lastModifiedDateTime, read off the same object
	// regardless of whether that object was found already existing or
	// just created - see each seed*() function's FindOrCreate call, which
	// returns the full Graph object on both paths for exactly this reason.
	//
	// Three container kinds have no date at all because Graph itself does
	// not return one for them: a mail folder (Kind "folder" under
	// WorkloadExchangeMail), a calendar (Kind "calendar" under
	// WorkloadExchangeCalendar) and a contacts folder (Kind "folder" under
	// WorkloadExchangeContacts) all model as a bare Graph Entity, not the
	// OutlookItem/BaseItem that carries createdDateTime/lastModifiedDateTime.
	// Every other item in this manifest - messages, events, contacts,
	// OneDrive folders and files, the SharePoint library and its files,
	// the SharePoint list and its items - does carry one.
	//
	// These are pointers, not plain time.Time, specifically so the three
	// dateless container kinds can be nil and genuinely omitted from the
	// JSON: encoding/json's "omitempty" never treats a zero-valued struct
	// (which time.Time is) as empty, only nil/zero-length/zero-number
	// values, so a plain time.Time field would always serialise even when
	// there is no real date to report.
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`

	// ResourceID is the ID of the mailbox, drive or site that owns this
	// item - i.e. Config.UserID, the OneDrive drive ID, or Config.SiteID.
	ResourceID string `json:"resource_id"`

	// Recurrence is non-nil only for the one recurring calendar event
	// this package seeds (Workload WorkloadExchangeCalendar, Kind
	// "event_recurring"). See RecurrenceDetail.
	Recurrence *RecurrenceDetail `json:"recurrence,omitempty"`
}

// RecurrenceDetail records what a later chunk needs in order to assert
// "listing a seeded mailbox matches the seed manifest" against a
// recurring series, not just its series master: which occurrence Graph
// no longer returns at all (cancelled), and which occurrence Graph
// returns with different content (an exception). Both fields are read
// through the typed Eventable.GetCancelledOccurrences()/
// GetExceptionOccurrences() fields the Graph SDK v1.104.0 upgrade (chunk
// 0.5) promoted out of AdditionalData - this manifest entry is the thing
// that actually exercises that migration against real Graph data, since
// every other seeded event is a single non-recurring occurrence.
type RecurrenceDetail struct {
	// CancelledOccurrenceIDs are this series'
	// Eventable.GetCancelledOccurrences() entries exactly as Graph returns
	// them (format "<seriesId>.<occurrenceStartDateTime>"). A later chunk
	// listing this calendar should find none of these IDs as a visible
	// event - that is what "cancelled" means. Always length 1 for this
	// seeded series (one deliberately cancelled occurrence), but a slice
	// because that is Graph's actual shape for the field.
	CancelledOccurrenceIDs []string `json:"cancelled_occurrence_ids"`

	// ExceptionOccurrenceIDs are the Graph IDs of this series'
	// Eventable.GetExceptionOccurrences() entries. Each one is also
	// present as its own ManifestItem (Kind "event_exception") elsewhere
	// in Manifest.Items, with its own Name/CreatedAt/ModifiedAt, so it can
	// be asserted against directly like any other item - this field is
	// just the index pointing at it. Always length 1 for this seeded
	// series, for the same reason as CancelledOccurrenceIDs.
	ExceptionOccurrenceIDs []string `json:"exception_occurrence_ids"`
}

// Manifest is the machine-readable record of everything the seeder put in
// the test tenant. The seeder writes one every run (see Write); 0.7 and 3.5
// read it back (see Load) to check that listing the tenant matches what was
// seeded.
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	TenantID      string         `json:"tenant_id"`
	Prefix        string         `json:"prefix"`
	Items         []ManifestItem `json:"items"`
}

// sortItems orders items deterministically so that two runs which seeded
// the same logical set of objects, in any internal iteration order, produce
// byte-identical JSON. This is what makes "diff the manifest from two runs"
// a meaningful repeatability check (Done-when #2).
func sortItems(items []ManifestItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]

		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}

		if a.FolderPath != b.FolderPath {
			return a.FolderPath < b.FolderPath
		}

		if a.Name != b.Name {
			return a.Name < b.Name
		}

		return a.ID < b.ID
	})
}

// NewManifest builds a Manifest from the items the seeder collected,
// stamping GeneratedAt with now and sorting Items for determinism.
func NewManifest(tenantID string, items []ManifestItem) Manifest {
	cp := make([]ManifestItem, len(items))
	copy(cp, items)
	sortItems(cp)

	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		TenantID:      tenantID,
		Prefix:        Prefix,
		Items:         cp,
	}
}

// Write serialises the manifest as indented JSON to path, creating any
// missing parent directories. It never writes a credential: Manifest and
// ManifestItem have no field capable of holding one.
func (m Manifest) Write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return clues.Wrap(err, "creating manifest directory")
	}

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return clues.Wrap(err, "encoding manifest")
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil { //nolint:gosec
		return clues.Wrap(err, "writing manifest")
	}

	return nil
}

// LoadManifest reads back a manifest previously written by Write. Later
// chunks (0.7, 3.5) use this to compare what they observe in the tenant
// against what the seeder recorded.
func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return Manifest{}, clues.Wrap(err, "reading manifest")
	}

	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, clues.Wrap(err, "decoding manifest")
	}

	return m, nil
}

// ItemsByWorkload returns only the items belonging to the given workload,
// in the manifest's stored (deterministic) order.
func (m Manifest) ItemsByWorkload(w Workload) []ManifestItem {
	var out []ManifestItem

	for _, item := range m.Items {
		if item.Workload == w {
			out = append(out, item)
		}
	}

	return out
}
