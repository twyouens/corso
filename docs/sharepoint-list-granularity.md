# SharePoint list granularity: findings

Chunk 0.11, a spike. It answers one question for chunks 5.1 (restore) and 7.3 (erasure): **can
row-level restore and row-level erasure of SharePoint list items be derived from the whole-list item
the engine stores today, or does the engine have to store each row as its own item?** PRD
requirements: RES-1, RES-4, GDPR-1, GDPR-2. Background: the implementation report's scope change
"SharePoint lists are list-granular in v1, with a spike before 5.1", and chunk 0.7's entry.

This document changes no product code. Line references are to this repository at `2c105291`
(`main` when the spike started) unless they name a dependency, and dependency references are to the
versions pinned in `src/go.mod`: Kopia `v0.23.1`, `kiota-serialization-json-go` `v1.1.5`. "Measured"
means the number comes from the live run in [section 5](#5-live-evidence).

## 0. The answer

| Question | Answer | Storage change needed? |
| --- | --- | --- |
| Row-level **restore** | **Yes, it can be derived.** The stored item holds every row, with its stable Graph ID and its field values, and the list's columns. The fork's existing restore path cannot write a subset into the original list today, but the gap is in the restore code, not in what was stored. | No |
| Row-level **erasure** | **Yes, it can be derived.** A point can be rewritten so that its list object no longer contains one row: decode the object, drop the row, re-encode it, and swap the new object into the snapshot with Kopia's `DirRewriter`. That uses the same machinery 7.3 needs for whole-item erasure, plus one content-transform step. Measured: removing a row leaves every other row and every non-row key unchanged. | No |
| Row-level **selection** (browse or search for a row, needed by both) | **Not possible from the details manifest today.** Browse reads only the details manifest, which records one entry per list with a row *count* and nothing else. Rows can be listed only by decrypting the list object, or by recording row summaries in the details at backup time. | No (details schema addition only) |

**Recommendation** ([section 6](#6-recommendation-and-sizes)): do not change the storage format.
Storing rows as items is the most expensive option (L–XL), and it is not needed for either
capability. If row-level work is wanted, build it from the stored item instead: a small
details-manifest addition, a medium restore path, and a small-to-medium transform added to 7.3. Whether
that work belongs in v1 is the user's decision, together with the PRD amendment that is still pending.
Four findings here matter to 5.1 and 7.3 even if lists stay list-granular. They are flagged with
**⚠** below.

## 1. What the backup stores for a list

### 1.1 Collection code: one list, one item

- `CollectLists` enumerates the site's lists, selecting only `id`, `list` and `lastModifiedDateTime`
  (`src/internal/m365/collection/site/backup.go:166`, `:176`). It skips templates listed in
  `api.SkipListTemplates` (`backup.go:233`; the set itself is at `src/pkg/services/m365/api/consts.go:102`).
- Each list becomes **its own collection**, stored under a folder named by the list's M365 ID
  (`storageDir = path.Elements{listID}`, `backup.go:238-239`, path built at `:256`). That collection
  holds **exactly one item**, whose ID is again the list ID and whose mod time is the list's
  `lastModifiedDateTime` (`backup.go:262-274`). If Graph returns a zero mod time, the code falls back to
  a prefetch collection with the same single item (`backup.go:280-297`).
- When Kopia reads that item, `lazyItemGetter.GetData` calls `GetItemByID`, which is
  `api.Lists.GetListByID` (`collection.go:500-504`, `lists_handler.go:42-47`). That call fetches the
  list, its columns, its content types (each with column links and columns), **every row**, and then
  **each row's `fields` with one GET per row** (`src/pkg/services/m365/api/lists.go:91-115`,
  `:120-167`, per-row fields at `:157-164`). For one list with *C* content types and *N* rows that comes
  to at least `4 + 2C + N` Graph requests, more if any collection pages. The seeded list (C=2, N=2) needs 10.

### 1.2 Serialised format

- The whole `models.Listable`, with its rows attached through `list.SetItems(lItems)`
  (`lists.go:112`), is written as one kiota JSON document (`serializeContent`,
  `collection.go:535-553`).
- A 4-byte big-endian header (a deleted-in-flight flag and a format version) is prepended to every
  stored item (`src/internal/common/readers/serialization_version.go:13-24`, injected at
  `src/internal/data/item.go:197-221`). On read it is stripped
  (`src/internal/kopia/data_collection.go:124`). The content between header and end is plain JSON.
- Measured, for the seeded list: 16,023 bytes of JSON. Top-level keys `columns` (24),
  `contentTypes` (2), `items` (2 rows), plus the list's own metadata (`id`, `displayName`,
  `createdBy`, `lastModifiedBy`, `list`, `webUrl`, …). The non-row part alone, re-marshalled, is
  12,775 bytes. Each row is about 1,618 bytes.

### 1.3 Rows and their IDs are present

Measured:

- Every stored row carries its Graph list-item ID **twice**, as `items[].id` and as
  `items[].fields.id`, together with its `eTag`, `createdDateTime`, `lastModifiedDateTime`,
  `createdBy`/`lastModifiedBy`, `contentType`, `webUrl`, and the full `fields` set (22 field names on the
  seeded `genericList`, among them `Title`, `AuthorLookupId`, `EditorLookupId` and `Attachments`).
- Both stored row IDs (`"6"`, `"7"`) and both stored eTags **equal what live Graph returns at the
  same time** for that list.
- Row IDs are SharePoint's own per-list integer IDs. They are assigned by the server and are not the
  fork's: `api.CloneListItem` never copies an ID (`lists.go:462-493`), so a restored row receives a
  **new** ID (measured: the cloned row has no ID). The seeded list's live rows are 6 and 7, not 1
  and 2. That fits SharePoint not reusing IDs after deletion, but this spike only observes it and does
  not prove it, because proving it would require a write.
- **Not stored:** row version history (no `versions` key on any row; `GetListItems` does not expand
  it) and attachment content (`Attachments` is only a boolean field; no attachment is ever fetched).

### 1.4 What the details manifest (browse) holds

- One details entry per list. Its `SharePointInfo.List` is `ListInfo{Name, ItemCount, Template}`
  (`src/pkg/backup/details/sharepoint.go:54-58`), filled by `api.ListToSPInfo`, which sets
  `ItemCount = len(lst.GetItems())` (`lists.go:786-811`, count at `:792`). **No row ID, title or
  date reaches the details.** `ListToSPInfo` also never sets `Size`, so browse reports `SizeBytes`
  0 for every list (measured).
- The selector layer has the same model. `SharePointListItem`, despite its name, is the **list**:
  its path keys are `{SharePointList, SharePointListItem}`
  (`src/pkg/selectors/sharepoint.go:499-500`), its item name is `List.Name` (`:586-593`), and the
  code says so outright: "list itself is the item -> so container and item are the same"
  (`:696-699`).
- Measured: `engine.ListItems` on the point returns **1** entry, `ItemCount` 2, under a folder
  named by the list ID. The entry has the same browse ID (`c4d7b61c296d`) in both points.

### 1.5 ⚠ Incremental backups reuse the list object by name and mod time only

- Corso hands each item to Kopia as a streaming file whose mod time is the item's mod time
  (`src/internal/kopia/upload.go:516-518`, `:548-551`). For a list that is the list's
  `lastModifiedDateTime`.
- For a **streaming** file, Kopia treats a previous snapshot's entry as a cache hit when only name,
  mode, owner and mod time match. Size is not compared (`kopia/snapshot/upload/upload.go:722-735`
  with `commonMetadataEquals` at `:694-708`). On a hit the item is never read, so the list is
  **never fetched from Graph** and the previous object ID is reused.
- Measured: the second backup recorded `persisted-cached-files=1` (the list) and
  `persisted-hashed-bytes=234`, against 16,261 for the first. Both points return byte-identical stored
  content.
- So between changes to the list's `lastModifiedDateTime`, **every backup point shares one Kopia
  object for the list.** Erasure has to cover all of them (section 3.4). This spike did not verify
  that a row edit always moves the list's `lastModifiedDateTime`, because that also needs a write.
  The seeded list's `lastModifiedDateTime` (17:09:30) is later than both rows' (16:42:07, 16:42:08),
  which fits that behaviour without proving it.

### 1.6 Serialisation is not byte-deterministic

- kiota writes `AdditionalData`, which is where every row's `fields` live, by ranging over a Go map
  (`kiota-serialization-json-go/json_serialization_writer.go:804-807`). Key order is therefore random
  on each serialisation.
- Measured: re-serialising the same parsed object twice gives different bytes. Two fresh Graph fetches
  give different bytes too, though they are semantically equal. Over five probe runs, five fresh first
  backups stored five different SHA-256 values, all at 16,023 bytes. The parsed stored object and a
  re-serialisation of it compare equal under `encoding/json` + `reflect.DeepEqual`. The only paths that
  differed between the stored copy and a separate fresh fetch were three `@odata.context` URLs.
- Consequence: whenever the cache in 1.5 misses, the whole list is stored again in full. Content-
  addressed deduplication does not help, even when no row changed. This is a storage-cost point, not a
  granularity one. It also means any rewrite (section 3) produces new bytes, which is harmless.

## 2. Restore

### 2.1 What the fork's list restore does today

`site.RestoreListCollection` (`src/internal/m365/collection/site/restore.go:150-235`) passes each
stored item to `restoreListItem` (`:33-109`). That function decodes the whole list
(`api.BytesToListable`, `:60`) and then:

- **Always creates a new list.** `rh.PostList` (`:87`, `:120`) goes to `api.Lists.PostList`
  (`lists.go:169-217`), which POSTs a new list named `<RestoreConfig.Location>_<displayName>`
  (`restore.go:69`, `:316-327`) and then POSTs **every** stored row into it (`lists.go:197-208`).
  No code path anywhere in the fork writes a row into an *existing* list.
- **⚠ With the default `Skip` policy it does nothing if the original list still exists.** The
  collision key is the stored list's display name (`api.ListCollisionKey`, `lists.go:820-826`). It is
  checked against the live site's lists (`restore.go:66`, `:72-81`; map built in
  `src/internal/m365/service/sharepoint/restore.go:102-107`), so the ordinary case of restoring from a
  list that still exists returns `ErrAlreadyExists` (`restore.go:76-81`, pinned by `restore_test.go:228`).
  `DefaultRestoreConfig` uses `Skip` (`src/pkg/control/restore.go:71`).
- **⚠ With `Replace` it deletes the live list.** `handleListReplace` creates the copy, **DELETEs the
  original list** and renames the copy (`restore.go:111-148`, delete at `:129`). That conflicts with
  RES-4 ("nothing is overwritten") and with the workspace rule against issuing a DELETE in the test
  tenant. 5.1 must never pass `control.Replace` for lists.
- **⚠ Row failures are silent past the first one.** `PostListItems` returns on the first failing row
  (`lists.go:224-236`). `PostList` records that as one recoverable error and still returns the list
  as restored (`lists.go:209-216`). Rows after the failure are never attempted, and nothing reports per
  row, which is what RES-6 asks for.

### 2.2 Can it write back a subset of rows? Not as built

- Nothing carries a row choice. `control.RestoreConfig` has `OnCollision`, `ProtectedResource`,
  `Location`, `Drive` and `IncludePermissions` (`src/pkg/control/restore.go:42-67`), and the
  selectors' finest list grain is the list itself (section 1.4).
- `PostList` always posts all of `storedList.GetItems()` (`lists.go:199-208`).

### 2.3 Is the stored data enough to do it? Yes

Measured, entirely in memory with no Graph write: the stored bytes decode through the fork's own
`api.BytesToListable`. One row picked out by its stored ID goes through `api.ToListable` +
`api.CloneListItem` (the exact transforms `PostList` uses) and gives a valid single-row POST body
(here `fields: [Title]`, since every other column on a `genericList` is read-only or legacy and is
filtered out by `ToListable`, `lists.go:311-332`). Everything a row-level restore needs to send is in
the stored item: the row's fields, the column definitions that decide which fields are writable, and
the row's ID, which is needed to detect a collision with a live row.

### 2.4 What row-level restore would take

All of this is restore-path code in `internal/m365/collection/site` plus a `pkg/engine` surface. None
of it touches backup or storage.

1. A row filter carried from the caller to `restoreListItem`, either as a new restore option or as a
   `pkg/engine` call taking `(BackupPointID, list Item.ID, []rowID)`.
2. **Write into the original list** (RES-4). Find it by the list's M365 ID, which is the stored item
   ID, falling back to the display name. If it is gone, recreate it with the existing `PostList` and
   post only the chosen rows. This is the one genuinely new Graph write path: a row POST into an
   existing list.
3. Map columns against the **live** list, not only the stored one. `CloneListItem` filters fields by the
   *stored* column names (`lists.go:522-550`), so a column renamed or deleted since the backup would
   fail the POST.
4. Row collision rule for RES-4. A row has no unique "name" (`Title` is not unique), so the natural
   rule is "a live row with the same ID still exists, so restore alongside it with a dated `Title`
   suffix". A restored row always gets a new ID (1.3), so repeating the restore cannot detect that it
   already ran. That needs a decision in 5.1's brief.
5. Per-row results and error handling. Replace `PostListItems`' stop-on-first-error (2.1) with a
   per-row result.
6. Lookup and person columns store `LookupId`s into other lists and the site's user list
   (`lists.go:392-396`, `:422-449`), and those IDs may be stale by restore time. The existing list
   restore already carries this risk; it is not new to rows.

**Restore answer: derivable, with no storage change. Rough size: M**, one engine chunk of a few
hundred lines plus a live integration test. Pre-requisite for selection: section 4, option B1.

## 3. Erasure

### 3.1 How a backup point is stored

A point is three Kopia manifests: the backup model (`ModelStoreID`), the **item-data snapshot**
(`SnapshotID`) and the **details/streamstore snapshot** (`StreamStoreID`)
(`src/pkg/backup/backup.go:24-40`). Deletion removes all three together
(`src/pkg/repository/backups.go:357-396`). The list is one file inside the item-data snapshot's tree,
at `…/lists/<listID>/<listID>`, whose content is the object from section 1.2. The details snapshot
holds its `ListInfo` (section 1.4).

### 3.2 Can a point be rewritten without one row? Yes

Kopia ships the primitive. `snapshotfs.DirRewriter` walks a snapshot tree and calls a
`RewriteDirEntryCallback` for each entry. The callback may return a **replacement** entry, for example
one with a new `ObjectID` and `FileSize`, or `nil` to drop it, and `RewriteSnapshotManifest` installs
the new root (`kopia/snapshot/snapshotfs/dir_rewriter.go:28-30`, `:43-51`, `:93-136`, `:226-246`;
constructor at `:326` takes a `repo.RepositoryWriter`). The fork already opens write sessions and saves
snapshot manifests (`src/internal/kopia/wrapper.go:350-406`).

A row-erasure callback would, for the list's file entry: open the object, strip the 4-byte header,
`api.BytesToListable`, drop the row(s) by ID, re-serialise, re-prefix the header, write a new object,
and return the entry with the new object ID and size.

Measured, in memory: removing row `"6"` from the stored seeded list (16,023 → 14,404 bytes) leaves the
remaining row **deep-equal** to its original and every non-`items` top-level key **deep-equal**. The
erased row's distinctive seeded `Title` was present before and is absent after. This is a bounded check
on a long, prefixed, unique string, not a short-needle search. `ListToSPInfo` of the result reports
`ItemCount` 1. The fork's serialiser round-trips the stored object with no semantic loss (1.6), so the
transform cannot damage the other rows' data.

### 3.3 Whole-item erasure needs most of this anyway

7.3 needs these steps for erasing *any* item (a mail message, a file, a whole list), whatever happens
with lists:

- rewrite the item-data snapshot with that entry removed (the `DirRewriter` callback returns `nil`);
- rewrite the details snapshot without its entry;
- repoint the backup model. The base finder skips a backup whose `SnapshotID` does not equal the
  snapshot manifest's ID (`src/internal/kopia/base_finder.go:208-214`), and finds the model through
  the snapshot's `TagBackupID` tag (`:126-135`), so a rewrite must update the model and keep the tags,
  or incremental chaining breaks;
- delete the old manifests and run full maintenance, whose safety margins (24 h minimum content age,
  two GC cycles: `kopia/repo/maintenance/maintenance_safety.go:58-67`) fit inside GDPR-2's 30 days.

Row erasure **adds** to that core only:

1. the content transform in 3.2 (replace an entry rather than drop it);
2. updating the list's details entry (`ItemCount`, and the row summaries if option B1 is built)
   instead of removing it.

### 3.4 Traps specific to lists

- **⚠ One object, many points.** Because of 1.5, an unchanged list is the *same* Kopia object in every
  point since its last change. A row erasure has to rewrite every point that references it, not only
  the one the admin browsed. `DirRewriter` caches replacements by a hash of the whole input entry
  (`dir_rewriter.go:78-91`, `:93-107`), so within one run each distinct list object is transformed once.
  Several points are still several snapshot and details rewrites.
- **⚠ The next backup can inherit the erasure silently.** If the rewritten entry keeps the original
  mod time, the next backup's cache check (1.5) matches on name + mod time, reuses the **row-less**
  object and never re-fetches the list. Every later point then lacks a row that still exists in
  Microsoft 365, until the list's `lastModifiedDateTime` moves. That is a backup-correctness defect,
  not an erasure feature (GDPR-5 says erasure *cannot* stop re-capture). The rewrite must give the
  replacement entry a mod time that cannot match, and 7.3's tests must assert the next backup
  re-fetches the list. Whole-item erasure removes the entry instead, so there is nothing to match and
  this trap does not arise.
- **Not everything about a row lives in its row.** A lookup column in *another* list stores the
  referenced row's `LookupValue` (its title) in that list's rows (`lists.go:392-396`). The list's own
  `lastModifiedBy` can name the person who last edited the erased row. Neither is removed by erasing the
  row, under any storage layout. 7.3's erasure report (GDPR-6) should say so rather than imply
  completeness.

**Erasure answer: derivable, with no storage change. Rough size: S–M on top of 7.3's rewrite core**,
which is L on its own and is needed regardless. The core should be built with a per-entry *transform*
hook, not only "drop entry", so that row erasure is an addition rather than a rework.

## 4. Selection, and the cost of storing rows as items

Restore and erasure of a row both need the admin to *find* the row (RES-1 browsing, GDPR-1 "by browsing
or by name", RES-2). Today nothing outside the encrypted list object names a row (1.4). The options:

**B1. Row summaries in the details manifest (no storage change).** At backup time, where
`ListToSPInfo` already has every row in hand, record `{rowID, Title, created, modified}` per row in
`ListInfo`, for example a new `Rows` field with `omitempty`. Browse and `FindItemsByName` can then list
and find rows without decrypting content, which keeps 0.7's guarantee that browse fetches no item
content. Older points lack the field, so they stay list-granular. Size: **S**. Erasure must also strip
the erased row's summary from the details (3.3, step 2).

**B2. Read the list object at browse time.** Browse decrypts one object per list (16 KB here). It
works on existing points but breaks 0.7's "no item content fetched or decrypted" property, so it is
not recommended.

**C. Store each row as its own item.** Collect each list as a collection holding a list-schema item
(columns and content types, without rows) plus one item per row, each with its own details entry and
mod time. What that touches:

- **Backup:** `populateListsCollections`, `lazyItemGetter`, `ListToSPInfo`, a new details `ItemType`
  and info struct, and a backup-format version bump. The incremental merge has to cope with a base
  whose `<listID>` folder holds the old single file. Benefit: per-row cache hits (1.5), so unchanged
  rows skip their per-row `fields` GET, and a one-row change stores about 1.6 KB instead of the whole
  list (16 KB here; 1.6).
- **Selectors:** `SharePointListItem` changes meaning from "list" to "row" (`sharepoint.go:499-500`,
  `:586-593`, `:696-699`). That is visible to the CLI and to every caller.
- **Restore and export:** they reassemble schema plus rows, and must keep restoring the old
  single-file layout for every pre-existing point indefinitely. Export today writes one
  `<listID>.json` per list (`src/internal/m365/collection/site/export.go:46`).
- **0.3's manifest: no schema change is needed.** It already records each row as a `list_item` with its
  Graph ID and the list name as `folder_path` (`src/internal/seed/sharepoint.go:259-307`). What changes
  is the assertions made against it. `browse_integration_test.go:529-575` asserts that the list is
  one item with `ItemCount` 2 and that **no row lists as an item of its own**, and option C inverts
  both.
- **0.7's browse output:** the list's container folder (still named by list ID) holds N row items
  plus a schema item, not one item. The guidance in `src/pkg/engine/README.md` §4.10 to "label a lists
  container from the single item inside it" stops working, `Item.ItemCount` becomes vestigial, and each
  row gets its own `ShortRef`. That uses up more of the 48-bit ID space the scope-change entry flags for
  7.3.
- **Erasure then needs no transform** (drop the row's entry, as for any item). Restore needs the same
  live-list writes as in 2.4, items 2–6, so C does not make restore cheaper.

Size: **L–XL**, several chunks across backup, details, selectors, restore, export and browse, plus
format versioning and permanent dual-layout support. It also diverges further from upstream Corso.

## 5. Live evidence

One read-only run against the test tenant's seeded list `k3ep-seed-list` on `SEED_SITE_ID`,
repeated five times while the probe was refined. Every run read the same list. Nothing was seeded,
written, modified or deleted in Microsoft 365. The probe never called `seed.Run` (which can write):
it found the list by display name with a Graph GET, backed it up twice with `engine.Backup`
(selector: that one list) into a local, disposable filesystem repository, browsed both points, read
the stored bytes back through the fork's own export path (`repository.NewExport`), and then did all
decoding, transforming and re-serialising in memory. It logged structure, IDs, counts, sizes and hashes,
never a field value. The probe was a throwaway `_test.go` and is **not committed**. Its source is
in the pull request description, for reproduction.

Output of the final run, with the list's M365 ID redacted to `<seed-list-id>`:

```
GRAPH list found: id=<seed-list-id> lists_on_site=3
GRAPH live rows: count=2 ids=[6 7]
BACKUP 1 count persisted-cached-files=0
BACKUP 1 count persisted-non-cached-files=2
BACKUP 1 count persisted-uploaded-bytes=9640
BACKUP 1 count persisted-hashed-bytes=16261
BACKUP 1: type=merge itemsRead=2 bytesUploaded=9640 errors=0
BACKUP 2 count persisted-cached-files=1
BACKUP 2 count persisted-non-cached-files=1
BACKUP 2 count persisted-uploaded-bytes=4905
BACKUP 2 count persisted-hashed-bytes=234
BACKUP 2: type=merge itemsRead=2 bytesUploaded=4905 errors=0
BROWSE point1: entries=1 id=c4d7b61c296d folderPath=<seed-list-id> nameIsListName=true itemCount=2 sizeBytes=0 category=lists
BROWSE point2: entries=1 id=c4d7b61c296d folderPath=<seed-list-id> nameIsListName=true itemCount=2 sizeBytes=0 category=lists
EXPORT point1: basePath=Lists/<seed-list-id> itemID=<seed-list-id> name=<seed-list-id>.json bytes=16023 sha=797e1c05fa63ea02
EXPORT point2: basePath=Lists/<seed-list-id> itemID=<seed-list-id> name=<seed-list-id>.json bytes=16023 sha=797e1c05fa63ea02
STORED identical across the two points: true
JSON top-level keys: [@odata.context @odata.etag @odata.type columns contentTypes createdBy createdDateTime description displayName eTag id items lastModifiedBy lastModifiedDateTime list name parentReference webUrl]
JSON columns=24 contentTypes=2 items(rows)=2
JSON size: whole=16023 schema-without-rows(re-marshalled)=12775
ROW 0: id="6" idMatchesLive=true eTagMatchesLive=true rowKeys=[@odata.etag @odata.type contentType createdBy createdDateTime eTag fields id lastModifiedBy lastModifiedDateTime parentReference webUrl] fieldNames=[@odata.context @odata.etag AppAuthorLookupId AppEditorLookupId Attachments AuthorLookupId ContentType Created Edit EditorLookupId FolderChildCount ItemChildCount LinkTitle LinkTitleNoMenu Modified Title _ComplianceFlags _ComplianceTag _ComplianceTagUserId _ComplianceTagWrittenTime _UIVersionString id] rowBytes=1618
ROW 1: id="7" idMatchesLive=true eTagMatchesLive=true rowKeys=[…same…] fieldNames=[…same…] rowBytes=1618
ROUNDTRIP unmodified: bytes=16023 identical=false
ROUNDTRIP unmodified: semantically equal (json DeepEqual) = true
DETERMINISM same-object-serialised-twice identical=false
DETERMINISM two-fresh-fetches: bytes1=15834 bytes2=15834 identical=false semanticallyEqual=true equalToStoredSemantically=false
DIFF stored-vs-fresh paths (3): [$.@odata.context $.items[0].fields.@odata.context $.items[1].fields.@odata.context]
TIMES list created=2026-10-17T16:36:27Z lastModified=2026-10-17T17:09:30Z
TIMES row 0 created=2026-10-17T16:42:07Z lastModified=2026-10-17T16:42:07Z
TIMES row 1 created=2026-10-17T16:42:08Z lastModified=2026-10-17T16:42:08Z
ERASE-TRANSFORM: erasedRowID="6" rowsBefore=2 rowsAfter=1 remainingRowsDeepEqual=true nonItemKeysDeepEqual=true erasedTitlePresentBefore=true absentAfter=true bytesBefore=16023 bytesAfter=14404 ListToSPInfo.ItemCount=1
SUBSET-RESTORE: newListColumns=0 chosenRowSourceID="7" clonedRowFieldNames=[Title] clonedRowHasID=false
```

Earlier runs stored SHA-256 prefixes `8a34dc39fb09bb21`, `36d417b813dac907` and `2a19250201dd1529` for
the same unchanged list, each 16,023 bytes. That is the non-determinism in 1.6. `persisted-uploaded-bytes`
on the second backup (4,905) is the details, metadata and model written for the new point. The list
content was not re-hashed (234 bytes hashed in total).

Where the live run and the code reading could have disagreed, they agree: rows and IDs are present
(1.3), one browse entry per list (1.4), cache reuse on mod time (1.5). The only thing the live run
showed that the code reading had not predicted was that serialisation is non-deterministic (1.6), which
the kiota source then explained.

**Not verified, and why:** that a row edit always moves the list's `lastModifiedDateTime` (1.5), and that
row IDs are never reused (1.3). Both need a write to the tenant, which this spike may not make. A
restore POST of a single row was likewise never sent, only built (2.3).

## 6. Recommendation and sizes

Sizes are rough. S is a small, single-concern chunk. M is a full chunk with a live integration test.
L is several chunks.

| Option | Restore (5.1) | Erasure (7.3) | Storage change | Size beyond list-granular |
| --- | --- | --- | --- | --- |
| **A. List-granular (as decided)** | Whole list. Still needs the ⚠ guardrails below. | Whole list = drop one entry, same as any item | None | 0 |
| **B. Derive rows from the stored item** | B1 details row summaries **S** + row-subset restore into original list **M** | B1 **S** (shared) + row transform on 7.3's core **S–M** | None (details field addition only) | ≈ S + M + S–M |
| **C. Rows as items** | Same live-list work as B, plus layout reassembly | Drop row entry, no transform | Yes, new layout + version bump + permanent dual-layout restore | **L–XL** |

**Recommendation.** Do not change the storage format: reject C for v1. Its cost lands in backup,
selectors, export and compatibility, and it buys nothing restore needs. Its one real advantage
(transform-free erasure, per-row incremental caching) does not justify that cost at v1 scale. If
row-level restore or erasure is wanted, B delivers both without a migration. In either case:

- **Brief 5.1 with these list facts, even list-granular:** never pass `control.Replace` (it DELETEs
  the live list, 2.1); do not use the default `Skip` for lists (it restores nothing while the original
  exists, 2.1); use `Copy` with a dated `Location` so the copy lands "alongside" (RES-4); surface
  per-row failures rather than `PostListItems`' silent truncation.
- **Brief 7.3's rewrite core with a per-entry transform hook**, not only "drop entry". Require it to
  rewrite **every** point sharing the object (3.4), to give a replaced entry a mod time that cannot
  match the cache (3.4), to update the backup model's `SnapshotID` and keep the snapshot's tags (3.3),
  and to state in the GDPR-6 report what row erasure cannot reach (lookup copies in other lists).
- **If row-level is brought into v1:** add B1 as a small engine chunk ahead of 5.x's browse UI and
  7.3, then row-subset restore as an engine chunk alongside 5.1, then the row transform inside 7.3.
  Points taken before B1 stay list-granular unless option B2 is accepted for them.
