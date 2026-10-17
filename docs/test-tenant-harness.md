# Test tenant harness

Chunk 0.3 adds a harness that fills the Microsoft 365 test tenant with
known, recognisably-prefixed data (`k3ep-seed*`) across the v1 workloads -
Exchange mail, calendar and contacts; OneDrive; SharePoint document
libraries and lists - and records exactly what it created in a manifest
that later chunks assert against.

## What the harness never does

- It never deletes, disables or otherwise modifies a user, group or site it
  did not create. It does not create any user, group or site either: it
  seeds dedicated, prefixed folders inside an **existing** user's mailbox
  and OneDrive, and a dedicated document library and list inside an
  **existing** SharePoint site. Both must already exist in the tenant; see
  [Required environment variables](#required-environment-variables).
- It achieves repeatability purely by **idempotent upsert** (find the
  seeder's own container or item by its fixed, prefixed name; reuse it if
  found; create it only if missing), never by deleting and recreating
  anything, for any resource type, at any level. See
  `src/internal/seed/upsert.go` for the one primitive (`FindOrCreate`) every
  workload's seeding code is built from.
- It never prints, logs or writes a credential value anywhere. Startup
  credential validation reports missing environment variable **names**
  only (see below).

## Required environment variables

| Variable | What it is |
| --- | --- |
| `AZURE_TENANT_ID` | The test tenant's Entra tenant ID. |
| `AZURE_CLIENT_ID` | The app registration's client ID. |
| `AZURE_CLIENT_SECRET` | The app registration's client secret. |
| `SEED_USER_ID` | An **existing, already-licensed** M365 user (UPN or object ID) whose mailbox and OneDrive the harness seeds dedicated folders into. The harness never creates this user. |
| `SEED_SITE_ID` | An **existing** SharePoint site (canonical Graph site ID) the harness adds a dedicated document library and list to. The harness never creates this site. |

Optional:

| Variable | What it is |
| --- | --- |
| `SEED_MANIFEST_PATH` | Where the seeder writes the seed manifest. Defaults to `seed-manifest.json` in the process's working directory. |

These three credential names (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`,
`AZURE_CLIENT_SECRET`) match this fork's `CLAUDE.md`. Earlier in this
chunk's development, `.secrets/test-tenant.env` briefly defined the same
three credentials as `M365_TENANT_ID`/`M365_CLIENT_ID`/`M365_CLIENT_SECRET`
instead; the user decided the guide's names win, and the env file was
renamed to match. If the seeder ever reports these three as missing,
check the env file hasn't reverted to the `M365_*` names.

On start-up, both `src/cmd/seed` and the integration test check that every
variable above (except the optional one) is set, and if not, report the
exact missing names - never a value - and exit non-zero. Demonstrated live,
from `src/`, with no credentials set:

```
$ go run ./cmd/seed
missing required environment variable(s): [AZURE_TENANT_ID AZURE_CLIENT_ID AZURE_CLIENT_SECRET SEED_USER_ID SEED_SITE_ID]
```

## Seeding the tenant

From `src/` (there is no `go.mod` at the corso repository root, only
here - `go run ./src/cmd/seed` from the root does not work), through the
workspace's credential loader:

```
cd src && GO111MODULE=on GOWORK=off <workspace>/scripts/with-test-tenant.sh go run ./cmd/seed
```

Before writing anything, it resolves and prints `SEED_USER_ID` and
`SEED_SITE_ID` to their real display name/UPN and site name - so if either
variable points at the wrong real person or site, that is visible
immediately, not discovered later:

```
target user: Jane Doe (jane.doe@contoso.onmicrosoft.com)
target site: Contoso Team Site (contoso.sharepoint.com,...)
```

It then creates (or, on a later run, finds and reuses) in `SEED_USER_ID`'s
mailbox and OneDrive:

- a mail folder `k3ep-seed-mail` with two known messages,
- a calendar `k3ep-seed-calendar` with one known non-recurring event
  (`k3ep-seed-event-001`) and one known recurring series
  (`k3ep-seed-event-recurring-001`, five daily occurrences, one
  cancelled, one turned into an exception - see
  [The recurring calendar event](#the-recurring-calendar-event) below),
- a contacts folder `k3ep-seed-contacts` with one known contact,
- a OneDrive folder `k3ep-seed` with three known files,

and in `SEED_SITE_ID`:

- a document library `k3ep-seed-library` with one known file,
- a list `k3ep-seed-list` with two known items.

After the target lines, it prints one line per workload stating how many
items it found already present versus how many it had to create - counts
and workload names only, never an item's name, subject, filename or a
credential:

```
exchange-mail: 3 found, 0 created
exchange-calendar: 3 found, 0 created
exchange-contacts: 2 found, 0 created
onedrive: 4 found, 0 created
sharepoint-library: 2 found, 0 created
sharepoint-list: 3 found, 0 created
```

This is deliberate: a human re-running the seeder or `make -C src
test-integration` themselves can check this output against whatever a
result report claims, rather than having to trust it.

Running the seeder again leaves the tenant unchanged: every container and
item is looked up by its fixed name before anything is created, so a
second run finds everything the first run made and creates nothing new.
This is what `src/internal/seed/upsert_test.go` proves directly (without
touching the tenant) and what `src/internal/seed/smoke_test.go`'s one
integration test proves by actually running the seeder twice against the
real tenant and diffing the two manifests - including, the first time an
item is genuinely new, across the create-then-find transition itself, not
just two runs that both found everything already there.

## The seed manifest

The seeder writes a JSON file (default `seed-manifest.json`, see
`SEED_MANIFEST_PATH` above) describing exactly what exists in the tenant
afterwards. Schema (`src/internal/seed/manifest.go`):

```json
{
  "schema_version": 3,
  "generated_at": "2026-01-01T04:00:00Z",
  "tenant_id": "...",
  "prefix": "k3ep-seed",
  "items": [
    {
      "workload": "exchange-mail",
      "kind": "folder",
      "id": "AAMk...",
      "name": "k3ep-seed-mail",
      "folder_path": "",
      "resource_id": "<SEED_USER_ID>"
    },
    {
      "workload": "exchange-mail",
      "kind": "message",
      "id": "AAMk...",
      "name": "k3ep-seed-mail-001",
      "folder_path": "k3ep-seed-mail",
      "size_bytes": 34,
      "created_at": "2026-01-01T03:58:12Z",
      "modified_at": "2026-01-01T03:58:12Z",
      "resource_id": "<SEED_USER_ID>"
    }
  ]
}
```

`workload` is one of `exchange-mail`, `exchange-calendar`,
`exchange-contacts`, `onedrive`, `sharepoint-library`, `sharepoint-list`.
`id` is the item's Graph ID, stable across seeder runs. `folder_path` is
the container name(s) from the root down to (and including) the item's
immediate parent. `resource_id` is the mailbox, drive or site that owns the
item (so a OneDrive file's `resource_id` is a drive ID, not `SEED_USER_ID`
itself).

`created_at`/`modified_at` are Graph's own `createdDateTime`/
`lastModifiedDateTime` (`*time.Time` in Go, omitted from the JSON
entirely when nil - see the schema version note below for why they are
pointers, not plain timestamps), read off the same Graph object whether
the item was found already existing or just created, through the single
`datedManifestItem` function every workload's `seed*()` goes through.
Three container kinds carry no date at all, because Graph itself returns
none for them: the mail folder (`workload: "exchange-mail"`, `kind:
"folder"`), the calendar (`workload: "exchange-calendar"`, `kind:
"calendar"`) and the contacts folder (`workload: "exchange-contacts"`,
`kind: "folder"`). Every other item - messages, events, contacts, the
OneDrive folder and its files, the SharePoint library and its files, the
SharePoint list and its items - does carry one.

This is proven two ways, not one, because a live tenant can only prove it
once: `dated_item_test.go` mocks the Graph object boundary directly (no
HTTP, no tenant) and asserts, for every one of the seven Graph types this
package reads dates from, that two independently-constructed objects with
the same dates - standing in for "what find() returned" and "what
create() returned" for what would be the same physical item - produce an
identical `ManifestItem`. That is a standing regression guard: the harness
is correctly forbidden from ever deleting its own seed items, so once an
item exists in the tenant, every future live run finds it on both
`FindOrCreate` calls and never exercises the create branch for it again -
a live proof of this property can only ever happen once per item, the
first time it's created, and cannot catch a future regression by itself.
`smoke_test.go`'s one-time live observation (one new, permanent OneDrive
file added specifically to force a genuine create-then-found transition)
is the complementary proof that the real Graph API actually behaves the
way the mocked test assumes it does.

### Schema versions

- **1** (chunk 0.3, first round): `CreatedAt`/`ModifiedAt` existed as
  plain (non-pointer) `time.Time`, but five of six workloads never
  populated them, and the sixth (calendar) only set one on the created
  branch - a found item and a created item disagreed. Not a usable
  contract; nothing should have depended on it, and nothing did.
- **2**: `CreatedAt`/`ModifiedAt` are `*time.Time` and genuinely
  populated as described above. A consumer written against version 1's
  non-pointer field will not compile/unmarshal against a version-2
  manifest. A consumer written against version 2 must treat a
  missing/null date as "this item's kind has no date in Graph" (the three
  container kinds above), not as an error.
- **3** (current): added `ManifestItem.Recurrence` (non-nil only on the
  one recurring calendar event, `kind: "event_recurring"`) and a new
  `kind`, `"event_exception"`, for that series' one exception occurrence
  as its own top-level item - see
  [The recurring calendar event](#the-recurring-calendar-event) below.
  Purely additive: every version-2 field keeps its meaning, and a
  consumer that ignores unknown JSON fields and never asks for either new
  `kind` is unaffected.

Items are always sorted the same way (by workload, then folder path, then
name, then ID) before being written, so two manifests from two runs that
changed nothing diff to nothing except `generated_at`.

Later chunks assert against this file directly
(`src/internal/seed.LoadManifest`): 0.7's acceptance is "listing a seeded
mailbox matches the seed manifest", and 3.5's is "results match the seed
manifest".

### The recurring calendar event

Chunk 0.5 upgraded the Graph SDK from v1.30.0 to v1.104.0, which promoted
`Event.CancelledOccurrences`/`ExceptionOccurrences` from beta-only
`AdditionalData` entries to typed SDK fields, and deleted the old
map-parsing code that used to read them. Nothing exercised that migration
against real Graph data until this addition: every other seeded event is
a single, non-recurring occurrence, so the typed fields were never
actually populated by anything this harness seeded.

`k3ep-seed-event-recurring-001` is a short daily series (five
occurrences) in `k3ep-seed-calendar`. The seeder cancels one occurrence
(a `DELETE` on that occurrence's own instance ID - Microsoft's documented
mechanism for cancelling a single occurrence of a recurring event, and a
legitimate operation on a series this harness created; not a workaround,
and the only delete anywhere in this package) and modifies a different
occurrence into an exception (a `PATCH` on its instance ID, changing only
its subject). Both actions happen once, inside the series' own
`FindOrCreate` create branch, so a later run that finds the series
already existing does not repeat them.

The series master appears in the manifest as:

```json
{
  "workload": "exchange-calendar",
  "kind": "event_recurring",
  "id": "AAMk...",
  "name": "k3ep-seed-event-recurring-001",
  "folder_path": "k3ep-seed-calendar",
  "created_at": "2026-01-01T03:58:12Z",
  "modified_at": "2026-01-01T03:58:13Z",
  "resource_id": "<SEED_USER_ID>",
  "recurrence": {
    "cancelled_occurrence_ids": ["OID.AAMk....2026-01-03"],
    "exception_occurrence_ids": ["AAMk..."]
  }
}
```

`recurrence.cancelled_occurrence_ids` is `Eventable.GetCancelledOccurrences()`
exactly as Graph returns it - a later chunk listing this calendar should
find none of these IDs as a visible event. `recurrence.exception_occurrence_ids`
is the Graph ID of each `Eventable.GetExceptionOccurrences()` entry; each one
is *also* present as its own top-level item, `kind: "event_exception"`,
with its own `created_at`/`modified_at` (read off the exception occurrence's
own Graph object, not the series master's), so it can be asserted against
directly like any other item:

```json
{
  "workload": "exchange-calendar",
  "kind": "event_exception",
  "id": "AAMk...",
  "name": "k3ep-seed-event-recurring-001 (exception)",
  "folder_path": "k3ep-seed-calendar",
  "created_at": "2026-01-01T03:58:13Z",
  "modified_at": "2026-01-01T03:58:14Z",
  "resource_id": "<SEED_USER_ID>"
}
```

`smoke_test.go`'s `requireRecurringEventReadsTypedOccurrences` is the
assertion that actually exercises the migration: it requires both
`cancelled_occurrence_ids` and `exception_occurrence_ids` non-empty on a
live run, and fails if the typed-field read were ever broken or reverted
to the old `AdditionalData` path.

## Running the integration tests locally

```
scripts/with-test-tenant.sh make -C worktrees/corso-0.3/src test-integration
```

(adjust the worktree path to wherever your corso checkout lives; from
inside the repository itself, `scripts/with-test-tenant.sh` is one level up
plus whatever path gets you to the workspace root). This is a separate
Makefile target from `make check`, which `ci.yml` runs on every pull
request and which never touches the test tenant - `test-integration` costs
real Graph calls and is not part of it.

`test-integration` sets `CORSO_NIGHTLY_TESTS=true` and runs
`go test ./internal/seed/...`, which is this chunk's one integration test:
a smoke test that seeds the tenant, confirms every workload produced at
least one manifest item, seeds it again, and asserts the two manifests are
identical except for `generated_at`. It does not run the backup/restore
integration suites elsewhere in this repository (those are owned by
chunks 0.5-0.7, exercise a different, pre-existing Alcion test tenant via
`internal/tester/tconfig`, and are out of this chunk's scope).

## Where integration tests actually run

Say this plainly, because it is a real, deliberate arrangement and not a
shortcut: **the test tenant's credentials are never held by GitHub.** The
user chose to keep them off GitHub entirely rather than add them as
repository secrets. That decision is final and is not something to design
around - there is no mechanism anywhere in this harness for injecting
alternate or fake credentials into CI, and `.github/workflows/test-tenant.yml`
does not run on a schedule, because a scheduled job with no credentials
would fail every single time, and a permanently-red scheduled job is noise
that gets ignored, which defeats the point of having one.

**Integration tests against the real test tenant run locally, on demand**,
by a human, through `scripts/with-test-tenant.sh`, exactly as described
above. That satisfies the PRD's BLD-5 requirement directly - it asks for
tests that "run nightly or on demand rather than on every change" - even
though it deviates from the implementation plan's stricter 0.3 row, which
specifically asked for a scheduled nightly job reading secrets; the user
approved that deviation explicitly, and the PRD takes precedence over the
plan.

`.github/workflows/test-tenant.yml` still exists, triggered only by
`workflow_dispatch`, for two reasons: it keeps the credential-name-only
failure message exercisable (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`,
`AZURE_CLIENT_SECRET` as secrets; `SEED_USER_ID`, `SEED_SITE_ID` as
variables - none configured on this repository today, so a manual run of
it will fail, correctly, naming exactly those five), and it gives this
harness a manual CI entry point for the day credentials are ever made
available to it (for example a self-hosted runner with network access to
the tenant). **It is a shape check and a manual entry point, not an
automated verification.** Nothing in CI proves the harness works against
real Graph - only a human running it locally does that.

**When to run the local integration tests:** before a gate in the
implementation plan, and before starting any chunk whose work depends on
this engine's backup/restore behaviour against real Microsoft 365 data
(the chunks that come after this one - 0.5 onward - and anything in k3ep
that assumes a seeded tenant exists in the shape this harness's manifest
describes).
