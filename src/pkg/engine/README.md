# The k3ep library API: `pkg/engine`

This is the guide for someone writing k3ep against this fork. It covers what
`src/pkg/engine` does, the exact calls to make, the eleven things about it that
a reasonable developer would otherwise assume the opposite of, how to pin a
release, and how to develop across both repositories with a Go workspace.

`pkg/engine` is the **only** package k3ep imports for backup work. Everything
else in `src/pkg` is either a type `pkg/engine` needs as input (`pkg/account`,
`pkg/storage`, `pkg/credentials`, `pkg/control`, `pkg/selectors`,
`pkg/logger`) or the fork's older CLI-era surface. In particular
`pkg/repository` is public but is not the k3ep entry point: it returns
`src/internal` types from several of its methods, which a caller in another
module cannot name, and it has none of `pkg/engine`'s guard rails. Import
`pkg/engine`.

- Reference documentation: `go doc github.com/twyouens/corso/src/pkg/engine`
  (run `cd src && GOWORK=off GO111MODULE=on go doc -all ./pkg/engine`). Every
  type and field carries its own doc comment; this guide does not repeat them.
- Compiled examples: `src/pkg/engine/example_test.go`. Every snippet below is
  copied from there, so the compiler keeps them honest.
- A runnable, real end-to-end program: `src/cmd/backupexample`.

---

## 1. Pinning a release

The fork's module path is `github.com/twyouens/corso/src` and the module lives
in the repository's `src/` subdirectory, so **its version tags carry a `src/`
prefix**: the release this guide documents is the git tag

```
src/v0.1.0
```

which Go resolves as module version `v0.1.0`. A bare `v0.1.0` tag would not be
a version of this module at all — Go would ignore it. (The same rule gives
`go.uber.org/zap/exp` its `exp/v0.3.0` tags, if you want to see it in the
wild.)

k3ep pins it with one command, run from the k3ep repository root:

```sh
go get github.com/twyouens/corso/src@v0.1.0
```

That records the pin in **k3ep's `go.mod`** (a `require github.com/twyouens/corso/src v0.1.0`
line) and its checksums in **k3ep's `go.sum`**. Those two files are the only
place the engine version is recorded, and they are what CI builds from. Do not
add a `replace` directive to k3ep's `go.mod`, and do not pin by commit
pseudo-version: REPO-5 asks for a tagged release, and a `replace` or a local
path would make k3ep's build depend on a working copy nobody else has.

Upgrading later is the same command with a later version. The fork is Apache
2.0 and the repository is public, so no `GOPRIVATE` or credential setup is
needed.

### Cutting a release (for whoever does the tagging)

Two different strings are involved, and conflating them is the one mistake that
is expensive to undo:

| | Value |
| --- | --- |
| git tag that must exist | `src/v0.1.0` |
| module version Go resolves it as | `v0.1.0` |
| command a consumer runs | `go get github.com/twyouens/corso/src@v0.1.0` |

They differ because the module is defined in the repository's `src/`
subdirectory. Go's own module reference puts it plainly: the module
subdirectory "also serves as a prefix for semantic version tags". A bare
`v0.1.0` tag is therefore not a version of `github.com/twyouens/corso/src` at
all — Go ignores it, and `go get …@v0.1.0` fails to resolve. (`v0.1` is not a
valid module version either: major, minor and patch are all required.)

The order matters, and so does the care:

1. Merge the release's pull request and wait for CI to be **green on `main`**.
   The tag has to point at the released commit, so nothing is tagged before
   then.
2. Tag that merge commit — `src/v0.1.0` — and push the tag. **A human does
   this; it is deliberately not an agent's job.**
3. Only then can k3ep run the `go get` line above.

**A published module version is effectively impossible to withdraw.** Once
`proxy.golang.org` has served a version it keeps serving it — the mirror's own
FAQ says it "aims to cache content in order to avoid breaking builds for people
that depend on your package, so this bad release may still be available in the
mirror even if it is not available at the origin" — and the checksum database
has already recorded that version's hash, so re-pointing the tag at a different
commit gives every later consumer a checksum mismatch rather than the new code.
The supported remedy for a bad release is a `retract` directive plus a *new*
version, never a deleted or moved tag. Which is why this step is done slowly.

## 2. Local development across both repositories (`go.work`)

A Go workspace lets a k3ep change and an engine change be developed together,
without a `replace` directive and without pushing a tag for every experiment.
From the workspace root that holds both checkouts:

```sh
go work init ./repos/corso/src ./repos/k3ep
```

which writes a `go.work` like:

```
go 1.27.2

use (
	./repos/corso/src
	./repos/k3ep
)
```

While that file is in scope, a build of k3ep uses the local fork checkout
instead of the pinned module version, so an engine change is visible to k3ep
immediately. `go.work` and `go.work.sum` are developer-local: they are not
committed to either repository.

**Pull requests must build without the workspace.** A change that only
compiles because of `go.work` is a change that breaks CI and breaks anyone
else's clone, so before opening a pull request in either repository, run the
build and tests with the workspace switched off:

```sh
GOWORK=off go build ./... && GOWORK=off go test ./...
```

In this fork those commands run from `src/` (there is no `go.mod` at the
repository root). Two local notes worth knowing:

- On a machine whose global Go configuration sets `GO111MODULE=off`, add
  `GO111MODULE=on` to every command above, or module resolution silently falls
  back to GOPATH mode and every import fails to resolve. Several agents have
  lost an hour to this.
- The fork's own `make check` (`cd src && make check`) is what CI runs: build,
  lint, staticcheck and unit tests, all with `GOWORK=off`.

## 3. The six capability areas

### 3.1 Opening a repository

Every setting comes from the caller. The `pkg/engine` package itself reads no
environment variable, no configuration file and no flag, so there is nothing
ambient to configure and nothing to get right by accident. The one exception is
transitive and is the fork's logger, not this package: see §4.11.

```go
acct, err := account.NewAccount(
	account.ProviderM365,
	account.M365Config{
		M365:          credentials.M365{AzureClientID: clientID, AzureClientSecret: clientSecret},
		AzureTenantID: tenantID,
	})

st, err := storage.NewStorage(
	storage.ProviderFilesystem,
	&storage.FilesystemConfig{Path: repoPath},
	storage.CommonConfig{
		Corso:       credentials.Corso{CorsoPassphrase: repositoryKey},
		KopiaCfgDir: repoPath + "/kopia-config",
	})

cfg := engine.RepositoryConfig{Storage: st, Account: acct, Options: control.DefaultOptions()}

repo, err := engine.InitializeRepository(ctx, cfg) // first use
repo, err := engine.OpenRepository(ctx, cfg)       // every later use
defer repo.Close(ctx)
```

For S3-compatible storage (including MinIO), swap the storage arguments:

```go
st, err := storage.NewStorage(
	storage.ProviderS3,
	&storage.S3Config{
		AWS:      credentials.AWS{AccessKey: accessKey, SecretKey: secretKey},
		Bucket:   bucket,
		Endpoint: endpoint, // empty means AWS itself
		Prefix:   prefix,
	},
	storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: repositoryKey}})
```

Notes that matter in production:

- `CorsoPassphrase` is the repository's **encryption key** — ENC-1's tenant
  data key. The engine never generates, defaults or persists it. Lose it and
  the repository is unreadable.
- `Account` is required even for a storage-only operation: the fork ties a
  repository's lifecycle to a verified Microsoft 365 connection and fetches a
  real access token while opening. That matches k3ep's model (one repository
  per tenant connection), so `pkg/engine` does not route around it.
- `InitializeRepository` against a location that already holds a repository,
  with the **same** key, reconnects and returns the same repository rather than
  failing. `engine.ErrRepositoryExists` means the repository exists and the
  connection then failed — in practice, a wrong key.
- `Close` releases the handle and leaves the stored data alone.

See `ExampleInitializeRepository` and `ExampleOpenRepository`.

### 3.2 Backing up, with caller tags and a progress callback

```go
sel := selectors.NewExchangeBackup([]string{userID})
sel.Include(sel.MailFolders(selectors.Any(), selectors.PrefixMatch()))
sel.DiscreteOwner = userID

point, err := repo.Backup(ctx, engine.BackupRequest{
	Selector: sel.Selector,
	Tags: map[string]string{
		"k3ep-tenant": tenantID,
		"k3ep-policy": policyID,
		"k3ep-object": objectID,
	},
	Progress: func(p engine.ProgressSnapshot) {
		// p.Counts is a map[string]int64 of real, server-confirmed counters.
		// p.Done is true on exactly one call: the last.
	},
})
```

- The callback runs on a goroutine `Backup` owns and **joins before
  returning**, so no call arrives after `Backup` returns. Calls are never
  concurrent with each other. Nothing in `pkg/engine` writes to stdout; this
  callback is the only progress channel there is.
- `Counts` keys vary by workload and are not guaranteed stable across engine
  versions. Treat an absent key as `0`, never as an error. No count carries an
  item name, subject or content.
- Tags are validated **before any upload starts**: reserved key
  (`ErrReservedTagKey`), reserved prefix (`ErrReservedTagPrefix`), more than 32
  tags (`ErrTooManyTags`), a key over 256 bytes or value over 1024 bytes
  (`ErrTagTooLarge`), or more than 4096 bytes of keys and values combined
  (`ErrTagsTooLarge`). k3ep's real tags are nowhere near those bounds.
- `BackupPoint` reports `ID`, `Type`, `Tags`, `CreatedAt`, `ItemsRead`,
  `BytesUploaded` and `ErrorCount`.
- **`control.Options.FailureHandling` decides what a recovered error does, and
  k3ep has to choose.** It is a `control.FailurePolicy` on the `Options` passed
  in `RepositoryConfig`, and `control.DefaultOptions()` sets
  `control.FailAfterRecovery`:

  | Policy | A recoverable error (one throttled item, say) means |
  | --- | --- |
  | `control.FailAfterRecovery` (**the default**) | `Backup` returns an **error and no `BackupPoint`** — and an **assist** point carrying your tags has still been persisted, holding what did upload (§4.3) |
  | `control.BestEffort` | `Backup` **succeeds**, returning a **merge** point with a non-zero `ErrorCount`: the point is independently restorable, and incomplete |
  | `control.FailFast` | the run stops at the first failure |

  So `ErrorCount == 0` is not a completeness guarantee under `BestEffort`, and a
  returned error is not evidence that nothing was written under the default. The
  classification is the fork's `isAssistBackup`/`isMergeBackup`, which both
  branch on this policy.
- **A `Backup` call that returns an error may still have left a real backup
  point behind** — see contract item 3 below. Record the tags you asked for, so
  you can find it.

See `ExampleRepository_Backup`, and `src/cmd/backupexample` for a version that
really runs.

### 3.3 Listing and deleting backup points by tag

```go
points, err := repo.ListBackupPoints(ctx, map[string]string{
	"k3ep-tenant": tenantID,
	"k3ep-object": objectID,
})

deleted, err := repo.DeleteBackupPoints(ctx, map[string]string{
	"k3ep-tenant": tenantID,
	"k3ep-object": objectID,
})
```

- Matching is **conjunctive, byte-exact and case-sensitive**: a point matches
  only if it carries every key/value pair supplied. It was attacked with NUL
  bytes, newlines, embedded JSON and homoglyph values in review and held.
- `DeleteBackupPoints` requires a non-empty filter (`ErrEmptyTagFilter`): there
  is no "delete everything" call, by design. It returns the IDs it actually
  deleted — use those for the audit event (AUD-1) rather than a separate,
  racy list-then-delete.
- `ListBackupPoints` accepts an empty filter, and that is the dangerous case:
  see contract items 1 and 3.

See `ExampleRepository_ListBackupPoints` and
`ExampleRepository_DeleteBackupPoints`.

### 3.4 Browsing a backup point's folder tree

```go
tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{
	BackupPointID: pointID,
	// AllowIncompletePoint: false -> ErrIncompleteBackupPoint for an
	// assist, preview or unclassified point.
})

children := map[string][]engine.Folder{}
for _, f := range tree.Folders {
	children[f.ParentID] = append(children[f.ParentID], f)
}
```

- Reads only the point's own details manifest: **no Graph call, no item content
  fetched or decrypted**. A mailbox that no longer exists in Microsoft 365 is
  still browsable.
- `Folders` is **flat** and deterministically ordered by `(Path, ID)`.
  Reconstruct the hierarchy from `ParentID` (`""` means top level), not from
  `Path`: `Path` is a display string joined with `/` and a folder name may
  itself contain `/`.
- `Service` and `Category` are load-bearing, not decoration. Exchange location
  prefixes are stripped, so a mail folder, a calendar and a contacts folder all
  arrive as top-level folders with nothing but a display name to tell them
  apart — and two of them may legitimately share that name.
- A folder appears only if the point recorded at least one item at or below it:
  a folder that was empty at backup time is not in the manifest.
- A OneDrive drive and a SharePoint **document library** are drives, not folder
  entries: each appears as a folder literally named `root:`, with the library's
  human name on `DriveName`.
- A SharePoint **list** does get a container folder, named by the list's
  Microsoft 365 ID rather than its title — see contract item 10.
- `Folder.SizeBytes` is a roll-up of everything at or below the folder, as the
  backup recorded it. It is `0` for item kinds that carry no size (Exchange
  calendars and contacts routinely), and for a folder of Teams chats it
  disagrees with the items beneath it, because the fork's roll-up counts a
  chat's messages rather than its bytes. Teams chats are outside k3ep's v1
  workloads.

See `ExampleRepository_ListFolders`.

### 3.5 Listing the items in a folder

```go
list, err := repo.ListItems(ctx, engine.ItemsRequest{
	BackupPointID: pointID,
	FolderID:      folderID, // empty: every item in the whole point
})
```

- Same reads as `ListFolders`: manifest only, no Graph, no content.
- A `FolderID` the point does not contain is `ErrFolderNotFound`, never a
  silently empty list. Folder IDs are only valid within the point they were
  listed from.
- `Item.Name` is the real display name — subject, filename, contact name,
  list title. Names belong in returned data: this code never puts one in an
  error or a log line, and k3ep should hold the same line.
- `Item.SizeBytes`, `CreatedAt` and `ModifiedAt` are what the backup recorded.
  A zero date means "not recorded" (normal for Exchange calendars and contacts
  folders), not an error.
- OneDrive permission sidecars and other metadata entries the fork stores
  alongside real items are never returned.
- **Store `(BackupPointID, Item.ID)` as the handle, never `Item.ID` alone** —
  contract item 4.

See `ExampleRepository_ListItems`.

### 3.6 Finding items by name

```go
res, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
	Tags:  map[string]string{"k3ep-tenant": tenantID, "k3ep-object": objectID},
	Name:  query,
	Match: engine.NameMatchContains, // zero value; NameMatchExact for whole-name
})

if len(res.UnreadableBackupPointIDs) > 0 {
	// The result is PARTIAL. Say so.
}
```

- Searches **names only** — never message bodies, file contents or
  attachments. It reads the points' details manifests and decrypts nothing.
  Full-text search is out of scope for v1 and this call could not do it.
- `Tags` is required and non-empty (`ErrEmptyTagFilter`), and `Name` is
  required (`ErrEmptyNameQuery`). An empty query never means "every item": call
  `ListItems` with no `FolderID` for that, so the intent is visible at the call
  site.
- Matching is case-insensitive by simple Unicode lower-casing, not
  locale-aware collation.
- By default only independently-restorable points are searched; points skipped
  for that reason are skipped **silently**, since one fragment among many
  points is not a reason to fail a search. `AllowIncompletePoints` includes
  them, and each result says which point it came from and what type that point
  is.
- **`UnreadableBackupPointIDs` must be surfaced** — contract item 5.
- Cost: one details manifest read per matching point. Scope the tags to one
  object; this is not a search box over a repository.

See `ExampleRepository_FindItemsByName`.

### 3.7 Maintenance

```go
err := repo.RunMaintenance(ctx, engine.MaintenanceOptions{
	Metadata: true,  // cheap, metadata-only pass
	Force:    false, // skip the "not too often" safety check
})
```

Kopia garbage collection and index compaction. It blocks until it finishes,
and the repository stays readable throughout: maintenance never drops a blob a
current backup point still references. Metadata-only passes are cheap and can
run often; a full pass (`Metadata: false`) still has to run regularly for
unreferenced blobs to actually be reclaimed.

See `ExampleRepository_RunMaintenance`.

### 3.8 The whole surface, at a glance

Types (`go doc` has the per-field detail):

| Type | What it is |
| --- | --- |
| `Repository` | an open handle; `InitializeRepository`/`OpenRepository` return one, `Close` releases it |
| `RepositoryConfig` | storage, account and `control.Options`; everything the caller supplies |
| `BackupRequest`, `BackupPoint` | one backup's inputs and its result |
| `BackupPointType` | `merge`, `assist`, `preview`, `unknown`; `IsIndependentlyRestorable()` |
| `ProgressFunc`, `ProgressSnapshot` | the progress callback and the counters it receives |
| `MaintenanceOptions` | `Metadata` and `Force` for `RunMaintenance` |
| `FolderTreeRequest`, `FolderTree`, `Folder` | browsing one point's folder tree |
| `ItemsRequest`, `ItemList`, `Item` | listing the items in a folder or a whole point |
| `FindItemsRequest`, `FindItemsResult`, `FoundItem` | searching names across a tag-scoped set of points |
| `NameMatch` | `NameMatchContains` (the zero value) or `NameMatchExact` |

Errors, all matchable with `errors.Is`:

| Error | Returned when |
| --- | --- |
| `ErrRepositoryExists` | `InitializeRepository` found a repository and then failed to connect to it — in practice a wrong key |
| `ErrMissingStorageCredentials` | an S3 configuration with a blank or whitespace-only access or secret key |
| `ErrReservedTagKey`, `ErrReservedTagPrefix` | a backup tag key the engine reserves (§4.2) |
| `ErrTooManyTags`, `ErrTagTooLarge`, `ErrTagsTooLarge` | tag count, per-tag size, or total size over the bounds in §3.2 |
| `ErrEmptyTagFilter` | `DeleteBackupPoints` or `FindItemsByName` with no tags |
| `ErrEmptyNameQuery` | `FindItemsByName` with an empty `Name` |
| `ErrMissingBackupPointID` | `ListFolders` or `ListItems` with no backup point ID; an empty ID never means "the latest point" |
| `ErrBackupPointNotFound` | no point in this repository has that ID |
| `ErrIncompleteBackupPoint` | the point is not independently restorable and the request did not set `AllowIncompletePoint` |
| `ErrFolderNotFound` | that folder ID is not in that backup point |

---

## 4. The operational contract

Eleven things this library does that are not what you would guess. Each one
cost a review round, or an adversarial attack, to establish. They are the
reason this document exists.

### 4.1 Tags are a label, not an authorisation boundary

One repository must hold **exactly one tenant**. The engine has no concept of
"my tenant": it stores and filters tags, and never interprets them. Tenant
separation is the storage location and the encryption key, not the tags on the
points inside.

`ListBackupPoints(ctx, nil)` — or with an empty map — returns **every backup
point in the repository**. That is deliberate (it is how a caller discovers
what exists at all) and it is the one call in the package with no tag-filter
guard.

What k3ep must do:

- One repository per tenant, always.
- Generate every tag value **server-side**, from k3ep's own records. Never put
  a tenant-supplied string into a tag value or a filter.
- Pass a tenant-scoped filter on **every** `ListBackupPoints`,
  `DeleteBackupPoints` and `FindItemsByName` call. Treat an unfiltered list as
  an administrative, platform-level operation, not a tenant-facing one.
- `ListFolders` and `ListItems` take a bare backup point ID and check **no**
  tags at all. A point ID is not self-authorising; holding one is not evidence
  of being entitled to read it. Establish that the point belongs to the
  requesting tenant — by listing it through that tenant's filter, or against
  k3ep's own records — before passing an ID in.

### 4.2 Tag keys are namespaced, and some keys are refused

k3ep's tag keys are **`k3ep-tenant`, `k3ep-policy` and `k3ep-object`**. The
namespacing is not style. These keys are rejected outright, before any backup
work starts:

| Rejected | Why |
| --- | --- |
| `tenant` | the fork's backup garbage collector reads it to decide which assist bases survive collection |
| any key starting with `sc-` | same garbage collector, for service/category |
| `service`, `backup-type` | drive the fork's merge/assist/preview base selection for every backup in the repository |
| `type`, `stableID`, `storeVersion` | Kopia's own model store rejects them — but only *after* a backup has uploaded a snapshot, orphaning it until a full maintenance pass collects it |

A caller tag on a reserved key would let the caller silently influence which of
its own content a later maintenance run keeps, which would make the engine's
"it never interprets tags" promise false. So the engine refuses up front with
`ErrReservedTagKey` or `ErrReservedTagPrefix` rather than letting a backup run
and then disappear.

Note the mismatch with the PRD and plan, which say "tenant, policy and object":
the keys on the wire are the namespaced ones.

### 4.3 `ListBackupPoints` returns assist and preview points, and only merge points are restorable

The listing is deliberately everything, not just complete backups. A single
recoverable error partway through a real backup — one throttled item — is
enough to persist an **assist** point carrying your tags and occupying real
storage, while `Backup` itself returns an error and no point. That is the
behaviour of the **default** `control.FailAfterRecovery` policy; under
`control.BestEffort` the same backup returns a *merge* point with a non-zero
`ErrorCount` instead, and no assist point is created. §3.2 has the table. Hiding such
points once made a tenant's own data undeletable; that was a HIGH finding, and
this is the fix.

So: `BackupPoint.Type` is one of `engine.BackupPointTypeMerge`,
`engine.BackupPointTypeAssist`, `engine.BackupPointTypePreview` or
`engine.BackupPointTypeUnknown`, and `BackupPointType.IsIndependentlyRestorable()`
is true for **merge only** — including for any future type the engine has not defined yet,
which is conservatively not restorable. An unversioned, untagged model
classifies as `unknown`, deliberately: a genuinely pre-v8 backup with no tag
defaults to merge, but a model whose version was never set is not trusted.

What k3ep must do:

- Never present a non-merge point as a point in time to restore from. Check
  `IsIndependentlyRestorable()`, not a type string comparison of your own.
- Do list and delete them: they are the tenant's data and they occupy storage.
  A retention or erasure job that filters them out leaves data behind
  permanently.
- `ListFolders` and `ListItems` refuse them by default with
  `ErrIncompleteBackupPoint`. `AllowIncompletePoint` exists because erasure
  needs to enumerate an assist point's items in order to name them; when it is
  set, check `FolderTree.IndependentlyRestorable` before showing the tree to
  anyone.

### 4.4 The durable item handle is `(BackupPointID, Item.ID)`

`Item.ID` alone is not an identity for "this item". It is derived at backup
time from the item's storage path and stored in that point's manifest, which
means:

- **OneDrive and SharePoint**: the path is built from the folder chain's
  **display names**. Renaming *or moving* any ancestor folder gives the same
  file a different ID in the next backup point.
- **Exchange**: the path elements are container IDs, so renaming a mail folder,
  calendar or contacts folder is safe — but moving one is not, because its
  parent chain changes.

A backup point is immutable, so an ID listed from a point keeps identifying
that item in that point forever. What changes is whether the same ID means
anything in a *different* point.

What k3ep must do: store the pair. Never use `Item.ID` as a primary key for an
item across points, never resolve an ID against a point it was not listed from,
and expect "the same file" to have different IDs in successive points after a
rename. Folder IDs behave the same way (`ListItems` returns
`ErrFolderNotFound` for a folder ID from another point).

Related ceiling, inherited from the fork rather than introduced here: these IDs
are `sha256` over the concatenated path elements truncated to **12 hex
characters (48 bits)**, with no separator between elements. Collisions are
theoretically reachable. Anything that deletes by ID should decide whether 48
bits is enough for it.

### 4.5 `FindItemsResult.UnreadableBackupPointIDs` must be surfaced

`FindItemsByName` does not fail when a matching point cannot be read — a point
deleted by retention between the listing step and the read, a damaged
manifest, unreachable storage. It searches the rest and names the ones it
skipped.

That means **an incomplete search returns successfully**. A UI that ignores the
field shows a confident, empty, wrong answer, in exactly the situation where a
wrong answer costs most: verifying that an erasure actually took effect, or
answering a legal-hold question.

What k3ep must do: treat a non-empty `UnreadableBackupPointIDs` as a partial
result everywhere it appears — in the API response, in the UI, and in the audit
record. The IDs are safe to log and are what an operator needs to retry or
report the gap. `res.Items` holds every match from the points that *were*
readable; that is still useful, as long as nobody mistakes it for the whole
answer.

### 4.6 Graph concurrency is per client, at most four in flight, and never shared

Every Microsoft Graph client this engine builds carries its own concurrency
limiter. The capacity is a property of that client, taken from the
`RepositoryConfig.Options.Parallelism.ItemFetch` its caller passed, and
clamped to **1-4** in-flight requests: a value below 1 or above 4 becomes 4,
and `control.DefaultOptions()` is 4.

- **Two repositories opened in one process do not interfere.** Each gets its
  own semaphore at its own capacity. Opening repository A with
  `ItemFetch: 1` does not narrow repository B opened afterwards with
  `ItemFetch: 4`, in either order, and an open that fails - including one
  that fails Microsoft 365 authentication - sets nothing for a later,
  successful one.
- **A limiter is always installed.** There is no option set, and no code path,
  that produces a Graph client without one; a client built with no options at
  all is bounded at 4.
- **"Client" is not "repository", and the difference is a factor of two.** One
  repository builds **two** limited Graph clients, each with its own semaphore
  at the capacity above: `Stable`, which carries metadata and enumeration
  calls, and `LargeItem`, which carries mail and event attachments and drive
  item content. So one repository at `ItemFetch: 4` has an in-flight ceiling of
  **4 metadata plus 4 large-item requests, not 4 in total** - and the
  Requester, a third client used for arbitrary URLs and large content
  downloads, is not limited at all (below). Size a worker from 8 per
  repository, not 4.
- **A process's total in-flight Graph requests therefore scale with the number
  of repositories**, not with a single process-wide ceiling: N tenants backing
  up concurrently get 2N independent limiters. The engine does not, and cannot,
  cap the process total.

What k3ep must do: size a worker by goroutine, not by process; treat
`Options.Parallelism.ItemFetch` as a per-repository setting that takes effect
for that repository; and when planning capacity, count **two** limiters of
that width per open repository plus an unlimited content-download client, not
one. Do not assume a process-wide ceiling exists, because none does - if a worker needs to bound its own total Graph pressure across
many tenants, that bound belongs in k3ep, above this API.

Two limits this does **not** change, both of which remain process-wide and
neither of which this engine exposes:

- The **per-minute rate limiters** in the fork's Graph middleware (a token
  bucket per service, and a sliding window for Exchange) are package-level
  and shared by every client in the process. They bound the request *rate*,
  not simultaneity, which is why widening concurrency per client does not
  widen the rate at which this process can hit Graph.
- The **Requester** - the client that fetches arbitrary URLs and large item
  content - has never carried a concurrency limiter, upstream included, and
  still does not. Content downloads are bounded by the rate limiter and by
  the collection layer's own `ItemFetch` worker pools, not by a semaphore.

(Earlier drafts of this guide, and the engine code itself, described a
single process-global clamp latched by the first repository opened in the
process. That was accurate for v0.1.0 and is no longer accurate: the
singleton, its `sync.Once`, `graph.InitializeConcurrencyLimiter` and
`api.InitConcurrencyLimit` are all gone.)

### 4.7 S3 requires explicit credentials; there is no fallback path

`AccessKey` and `SecretKey` are required for every S3 configuration, for every
caller in this fork, with **no opt-out anywhere** — no flag, no config-map key,
no field. A blank or whitespace-only credential is rejected before any network
call, with `engine.ErrMissingStorageCredentials` from `pkg/engine` and a
`pkg/storage` validation error from everything else.

There is no ambient path: no EC2/ECS instance role via IMDS, no shared
credentials file, no `AWS_PROFILE`, no SSO, no web identity. Environment
variables work in the **CLI** only because the CLI reads
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` itself and the values are then
validated. No code path in `pkg/engine` reads either variable, or any other
*credential* from the environment (the fork's logger does read the environment
for its own file location — §4.11).

This was not a precaution. Kopia's S3 blob store falls through its AWS SDK's
own credential chain for any credential left blank, and a review proved it
live: a caller-chosen bucket, prefix and endpoint served by the *platform's*
identity, 13 requests signed with a credential the caller never supplied.

What k3ep must do: always pass real, server-derived credentials. If a
credential is missing, fail the operation — the engine has already failed it
for you.

### 4.8 `RepositoryConfig` must never be logged or serialised casually

`RepositoryConfig` embeds `storage.Storage` and `account.Account`, each of
which carries a credential — the repository's encryption key and the Microsoft
365 client secret — in a plain exported `map[string]string`.

Redaction is **per-encoder**, and only these encoders are covered. Measured
from outside the module, against a config carrying a marked fake key and
secret:

| Encoder | Result |
| --- | --- |
| `fmt` `%v`, `%+v`, `%#v`, `%s` | redacted (`String`, `GoString`) |
| `encoding/json`, directly or embedded in another struct | redacted (`MarshalJSON`) |
| `log/slog` | redacted (`LogValue`) |
| `zap`, `clues` | redacted (they route through `Stringer`/`LogValuer`) |
| **`gopkg.in/yaml.v3`, directly or embedded** | **leaks the key and the secret** |
| **`encoding/gob`** | **leaks the key and the secret** |

YAML and gob walk the exported maps and consult none of those methods. Nothing
in the fork marshals either, so this is k3ep's constraint to keep.

What k3ep must do: **persist an opaque reference, not the config.** Store a
storage-destination record plus a key reference, and rebuild the
`RepositoryConfig` from the key manager at the moment of use — which is what
ENC-1 implies anyway. This matters most where a config might get swept into a
job payload: k3ep's job queue persists payloads to Postgres. If something must
be serialised, JSON only, never YAML, never gob, and never a format added later
without re-running this check.

Two related defaults, inherited from the fork rather than added by
`pkg/engine`:

- With no logger configured, the fork's own singleton logger writes info-level
  logs to a file under the process user's log directory, and its PII handling
  defaults to **plaintext**, so `clues.Hide(...)`-wrapped values (tenant and
  resource owner IDs the fork's lower layers add) go out in the clear.
- Configure logging before the first engine call and pass the returned context
  in:

  ```go
  ctx, _ = logger.Seed(ctx, logger.Settings{
  	File:        logger.Stderr,
  	Format:      logger.LFJSON,
  	Level:       logger.LLInfo,
  	PIIHandling: logger.PIIHash,
  })
  ```

### 4.9 k3ep must derive storage location and credentials server-side

The engine trusts `RepositoryConfig` completely. It cannot know whether a value
is correct for the caller's tenant, and it does not guess. Stated plainly, by
review, this is what the struct hands whoever fills it in:

- a **repository-existence and key-validity oracle** — its errors distinguish
  "wrong key" from "not initialised";
- an **arbitrary-write primitive** at any filesystem path the process can
  reach: `InitializeRepository` creates the repository, files and all, if
  nothing is there yet;
- an **unvalidated outbound connection to any S3-compatible endpoint it is
  given**, including an internal, loopback or link-local address, with the
  responder's own error body reflected back to the caller.

None of that is a defect in the engine to fix: it is the direct consequence of
"the caller supplies everything" (REPO-3), which k3ep needs in order to open
its own tenants' repositories.

What k3ep must do: derive **every part** of `Storage` and `Account` server-side
— provider, path or bucket, endpoint, prefix, credentials, key, tenant ID —
from its own records, and let no tenant-supplied value reach any of those
fields, not even indirectly as a path or prefix fragment. If a deployment ever
needs operator-configurable endpoints, allowlist them (and block loopback and
link-local) on k3ep's side; the engine will not. Two further enforcement
measures belong to k3ep rather than here: a CI check forbidding
`pkg/repository` imports, and a configured logger with PII hashing.

### 4.10 A SharePoint list is one item

The fork records a whole SharePoint list as a **single backed-up item**
carrying the list's name, dates and the number of rows it held
(`Item.ItemCount`). Rows are not individually browsable and not individually
addressable: the list is the unit that is listed, restored and erased.
Browse, restore and erasure are therefore **list-granular for lists in v1** —
a decided scope change, with a spike to follow before restore is built, not an
oversight.

A list's container folder is named by the list's **Microsoft 365 ID, not its
title** (`Category` is `"lists"`). A naive tree UI shows a GUID.

What k3ep must do: label a `"lists"` container from the single item inside it,
not from the folder's name; show `ItemCount` so an admin can recognise the
list; and do not offer row-level selection for lists. Mail, calendar, contacts,
OneDrive and document libraries are unaffected — this is SharePoint lists only.

### 4.11 The engine reads nothing ambient — except the logger it inherits

Everything the engine itself needs comes from the caller: storage destination,
credentials, the repository key, Microsoft 365 application credentials, tags,
parallelism and the progress callback. `pkg/engine`'s own source contains no
`os.Getenv`, no configuration-file read and no flag registration, and it never
writes to stdout.

**One thing underneath it is environment-derived, and it is the logger.** On a
context that has not been seeded with a logger, the fork's lower layers call
`logger.Ctx`, which falls through to a process-wide singleton built from
`Settings{}.EnsureDefaults()` — and that path reads the environment and touches
the filesystem (`pkg/logger/logger.go`):

- `GetLogFile("")` reads **`CORSO_LOG_FILE`**;
- failing that, `defaultLogLocation()` builds a path under a per-user log
  directory taken from **`$HOME`** (macOS: `$HOME/Library/Logs`),
  **`$LOCALAPPDATA`** (Windows) or **`$XDG_CACHE_HOME`**, falling back to
  `$HOME/.cache` (other Unix);
- and `GetLogFile` then **creates that directory** with `os.MkdirAll`, so a
  first engine call on an unseeded context writes a log file into the process
  user's home directory;
- `PIIHandling` defaults to `PIIPlainText`, so `clues.Hide(...)` values go out
  in the clear (§4.8).

So the precise claim is: *the engine* reads nothing ambient, but *a process
using it without configuring logging* does. Seed a logger before the first
engine call (§4.8 has the call) and all four of those go away: the context
carries your logger, your destination, your format and your PII handling.

The rest of the obligation is the mirror image of the upside: there is no
ambient state to misconfigure and nothing environment-dependent to reproduce,
but if k3ep does not supply a value, no default rescues it.

---

## 5. What v0.1 deliberately does not do

- **No restore and no export.** `pkg/engine` reads backup metadata and nothing
  else. The fork's older `pkg/repository` can restore, but it is not the k3ep
  API and returns `internal` types; k3ep's restore arrives as its own chunk,
  behind `pkg/engine`.
- **No item-level erasure or snapshot rewriting.** Deleting a whole backup
  point by tag is all that exists today.
- **No row-level SharePoint list handling** (item 10).
- **No process-wide Graph concurrency ceiling.** v0.1.0 had one, by accident: a
  single process-global clamp of at most four in-flight Graph requests, latched
  by the first repository opened. That is gone as of the next release - see
  item 6 - and nothing replaced it, because per-client limiters are the right
  shape and a process-wide bound across tenants belongs in k3ep's scheduler,
  not in a library that has no notion of a tenant.
- **No full-text search.** Names only, and nothing in the browse API opens item
  content.
- **No scheduling, no retention policy, no catalogue, no multi-tenancy.** The
  engine has no notion of a tenant, a policy, a schedule or a database. Tags
  are strings it stores for you.
- **No ambient credential discovery of any kind** (items 7 and 11).
- **No S3 `AssumeRole`**, and S3 TLS handshakes use Go's default 10-second
  timeout rather than the 60 seconds Alcion's Kopia fork used to force; current
  upstream Kopia offers no injection point for it.
- **No usage telemetry.** It was removed outright; there is nothing to opt out
  of.

## 6. Verifying the surface yourself

Go's own rule — a module may not import another module's `internal` packages —
is what REPO-2 actually rests on, and it only fires across module boundaries.
The fork's in-repo checks (`src/cmd/backupexample/import_test.go`,
`src/pkg/engine/exported_api_test.go`) are syntactic and complementary; the
compiler is the authority. To reproduce that authority against this release,
build a throwaway module outside the fork:

```sh
mkdir /tmp/enginecheck && cd /tmp/enginecheck
cat > go.mod <<'EOF'
module enginecheck

go 1.27

require github.com/twyouens/corso/src v0.1.0
EOF
cat > main.go <<'EOF'
package main

import (
	"context"
	"fmt"

	"github.com/twyouens/corso/src/pkg/engine"
)

func main() {
	var (
		open  func(context.Context, engine.RepositoryConfig) (*engine.Repository, error) = engine.OpenRepository
		find  func(context.Context, engine.FindItemsRequest) (engine.FindItemsResult, error)
		merge engine.BackupPointType = engine.BackupPointTypeMerge
	)

	fmt.Println(open != nil, find == nil, merge.IsIndependentlyRestorable())
}
EOF
GOWORK=off GO111MODULE=on go mod tidy && GOWORK=off GO111MODULE=on go build ./...
```

Declaring each symbol with its type written out — rather than letting `:=`
infer it — is the point: inference is the "escape hatch" that lets a caller use
a value of an unnameable type without ever importing it, so a documented
signature is only really proven by naming it.

Then prove the other half, that the boundary holds:

```sh
cat > internal_probe.go <<'EOF'
package main

import _ "github.com/twyouens/corso/src/internal/model"
EOF
GOWORK=off GO111MODULE=on go build ./...   # must fail
```

It fails with `use of internal package github.com/twyouens/corso/src/internal/model not allowed`.

Chunk 0.8 ran exactly this against the release candidate, naming every exported
symbol in `pkg/engine` — all 13 error values, both constructors, all nine
`*Repository` methods, every exported type with each of its documented fields
read through a named type, `BackupPointType.IsIndependentlyRestorable` and
`RepositoryConfig`'s four redaction methods — plus a deliberately wrong symbol
as a negative control, to prove the build would have noticed. The surface
compiled, the wrong symbol failed, and both internal imports were refused.
