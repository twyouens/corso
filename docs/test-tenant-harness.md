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

This creates (or, on a later run, finds and reuses) in `SEED_USER_ID`'s
mailbox and OneDrive:

- a mail folder `k3ep-seed-mail` with two known messages,
- a calendar `k3ep-seed-calendar` with one known event,
- a contacts folder `k3ep-seed-contacts` with one known contact,
- a OneDrive folder `k3ep-seed` with two known files,

and in `SEED_SITE_ID`:

- a document library `k3ep-seed-library` with one known file,
- a list `k3ep-seed-list` with two known items.

Running it again leaves the tenant unchanged: every container and item is
looked up by its fixed name before anything is created, so a second run
finds everything the first run made and creates nothing new. This is what
`src/internal/seed/upsert_test.go` proves directly (without touching the
tenant) and what `src/internal/seed/smoke_test.go`'s one integration test
proves by actually running the seeder twice against the real tenant and
diffing the two manifests.

## The seed manifest

The seeder writes a JSON file (default `seed-manifest.json`, see
`SEED_MANIFEST_PATH` above) describing exactly what exists in the tenant
afterwards. Schema (`src/internal/seed/manifest.go`):

```json
{
  "schema_version": 1,
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

Items are always sorted the same way (by workload, then folder path, then
name, then ID) before being written, so two manifests from two runs that
changed nothing diff to nothing except `generated_at`.

Later chunks assert against this file directly
(`src/internal/seed.LoadManifest`): 0.7's acceptance is "listing a seeded
mailbox matches the seed manifest", and 3.5's is "results match the seed
manifest".

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

## The nightly job

`.github/workflows/nightly-test-tenant.yml` runs `test-integration` on a
schedule (04:00 UTC daily) and on demand (`workflow_dispatch`). Repository
secrets required: **`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`,
`AZURE_CLIENT_SECRET`**. Repository variables (not secrets, since they are
not sensitive) required: **`SEED_USER_ID`, `SEED_SITE_ID`**. If any secret
is missing, the step fails with the same name-only message shown above,
not a stack trace. Results are reported as the job's pass/fail status, an
uploaded `nightly-test-tenant-log` artifact, and a short job summary.

This workflow has been validated as valid YAML and reviewed step-by-step
against the existing `ci.yml`/`nightly_test.yml` patterns in this
repository, but **has not been run** (no real schedule has fired, and no
`workflow_dispatch` run has been triggered) - true completion needs a human
to configure the five repository secrets/variables above and either wait
for the schedule or trigger it manually.
