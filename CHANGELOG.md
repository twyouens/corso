# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] (beta)

Nothing yet.

## [v0.1.0] (beta) - 2026-10-18

**The first tagged release of this fork, and the first aimed at k3ep as a Go
library rather than at the Corso CLI.** The git tag is `src/v0.1.0`: the module
lives in this repository's `src/` subdirectory, so its version tags carry that
prefix, and Go resolves it as module version `v0.1.0`. A consumer pins it with
`go get github.com/twyouens/corso/src@v0.1.0`, which records the pin in the
consumer's own `go.mod`/`go.sum`; no `replace` directive is needed or wanted.
The guide for library callers is [`docs/library-api.md`](docs/library-api.md),
which also covers local development across both repositories with a Go
workspace and the `GOWORK=off go build ./... && GOWORK=off go test ./...` check
every pull request has to pass without one.

This fork restarts its own version numbering at v0.1.0. The `v0.19.0` and
earlier sections below are upstream Corso's history, kept for provenance; they
describe a CLI product, not this library API, and their version numbers are not
comparable to this one.

### What v0.1 offers

- **`pkg/engine`: the single public API a consumer imports.** Open or create a
  repository with caller-supplied storage settings and encryption key; back up
  a `pkg/selectors` selection with caller-supplied tags and a progress
  callback; list and delete backup points by tag; browse a backup point's
  folder tree, list a folder's items and find items by name; run Kopia
  maintenance. Every exported signature uses plain Go types or other public
  `src/pkg` types, so a caller in another module never has to name - and,
  because Go forbids importing another module's internal packages, never can -
  an `src/internal` type. Compiled examples live in
  `src/pkg/engine/example_test.go`; `src/cmd/backupexample` is the same thing
  as a program that really runs.
- **A library, not an application.** `pkg/engine` reads no environment
  variable, configuration file or flag, never writes to stdout, and reports
  progress only through the caller's callback. The caller supplies everything.
- **Browse without Graph.** The folder tree, item listings and name search read
  only a backup point's own details manifest: no Microsoft Graph call, no item
  content fetched or decrypted. A mailbox that no longer exists in Microsoft
  365 is still browsable, and a search never decrypts stored data.
- **Credentials and keys are the caller's, explicitly.** S3 storage requires a
  non-blank access key and secret key everywhere in this fork, with no opt-out
  and no fallback to an ambient AWS identity. `RepositoryConfig`,
  `storage.Storage` and `account.Account` redact their credential maps under
  `fmt`, `encoding/json`, `log/slog`, `zap` and `clues`.
- **A current dependency set.** Upstream Kopia v0.23.1 (Alcion's Kopia fork and
  its `replace` directive are gone), Microsoft Graph SDK v1.104.0 with current
  Kiota, MSAL v1.10.1, Go 1.27, and no `replace` directive on any dependency.
  Usage telemetry is removed outright rather than made opt-out.
- **A test-tenant harness.** `src/cmd/seed` fills a Microsoft 365 test tenant
  with known, prefixed data by idempotent upsert - it never deletes - and
  writes a manifest that tests assert exact results against.

### What v0.1 deliberately does not do

- **No restore and no export through `pkg/engine`.** It reads backup metadata
  and deletes whole backup points; that is all. The fork's older
  `pkg/repository` can restore and export, but it is not the library API: it
  returns `src/internal` types a caller in another module cannot name.
- **No item-level erasure and no snapshot rewriting.** Deleting a whole backup
  point by tag is the only removal this release has.
- **No row-level handling of SharePoint lists.** The engine records a whole
  list as one backed-up item with a row count, so browse is list-granular and
  restore and erasure will be too, pending a spike.
- **No full-text search.** `FindItemsByName` matches names only.
- **No notion of a tenant, policy, schedule, catalogue or database.** Tags are
  strings this library stores and filters on and never interprets - which also
  means **tags are not an authorisation boundary**: one repository must hold
  exactly one tenant, and `ListBackupPoints` with no filter returns every point
  in the repository.
- **No per-client Microsoft Graph concurrency control.** Opening a repository
  installs a *process-global* semaphore of 1-4 in-flight Graph requests across
  every service, shared by every tenant and concurrent backup in the process,
  latched by the first repository opened - including one that then fails
  Microsoft 365 authentication. A per-client replacement is planned.
- **No ambient credential discovery, anywhere.** No EC2/ECS instance role, no
  shared credentials file, no `AWS_PROFILE`, no SSO, no web identity - for the
  library or the CLI. Credentials may still be *supplied* to the CLI by flag,
  config file or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`.
- **No S3 `AssumeRole`,** and S3 TLS handshakes use Go's default 10-second
  timeout rather than the 60 seconds Alcion's Kopia fork forced; current
  upstream Kopia exposes no injection point for it.
- **Redaction is per-encoder and does not cover YAML or `encoding/gob`,** both
  of which walk the exported credential maps. Measured from outside the module.
  Persist an opaque reference and rebuild the config from a key manager at use
  time; if something must be serialised, JSON only.
- **No stability promise beyond `src/pkg`.** `src/internal` is not importable
  from another module and its shape will change without notice.

The eleven operational facts a library caller has to know - including the ones
above - are set out, with what to do about each, in
[`docs/library-api.md`](docs/library-api.md) section 4.

### Added
- Added [`docs/library-api.md`](docs/library-api.md), the guide for a consumer writing against `pkg/engine`: the call shape for each capability, the eleven operational facts a caller has to know (tags are not an authorisation boundary; the namespaced tag keys and the reserved ones; assist and preview points in every listing and only merge points restorable; `(BackupPointID, Item.ID)` as the durable item handle; `FindItemsResult.UnreadableBackupPointIDs` as a partial-result signal; the process-global Graph concurrency clamp; explicit-credentials-only S3; redaction covering `fmt`/`json`/`slog`/`zap`/`clues` but not YAML or `encoding/gob`; server-side derivation of storage location and credentials; a SharePoint list as a single item; and the library reading no environment variable, file or flag), the `go.work` and `GOWORK=off` instructions for local development across both repositories, the `go get` line that pins a release, and a recipe for re-proving the public surface from an out-of-module build. Added `src/pkg/engine/example_test.go`, a compiled godoc example per capability area (no `Output:` comment, so they are compiled and never run), which is where every snippet in the guide comes from; and `src/pkg/engine/docs_test.go`'s `TestLibraryAPIDoc_MatchesExportedSurface`, which fails if the guide names an `engine.Symbol` or a `Type.Member` that does not exist, or if an exported top-level declaration is named nowhere in the guide.
- Audited the Alcion Kopia fork pinned in `src/go.mod` against its upstream base; see `docs/kopia-fork-audit.md` for the fork-only dependency inventory and the recommendation to move to current upstream Kopia.
- Renamed the Go module path from `github.com/alcionai/corso/src` to `github.com/twyouens/corso/src` for this fork. Every import across `src/` was updated to match; no other behavior changed.
- Bumped the module's `go` directive (and CI's Go toolchain) from 1.21 to 1.27 (pinned via a separate `toolchain go1.27.2` line), the current stable release available to this fork, and fixed the handful of new `go vet` findings (non-constant printf format strings) and a `staticcheck` finding (unused context) surfaced by the newer toolchain.
- Migrated `src/.golangci.yml` to the golangci-lint v2 configuration schema and pinned golangci-lint to v2.15.0, since the previously pinned v1.54.2 cannot type-check code built with Go 1.27. Added a `make check` target (build, lint, unit tests) and a minimal CI workflow that runs it on pull requests and pushes to `main`.
- Removed Corso's usage telemetry entirely: the RudderStack analytics event bus (`internal/events.Bus`), its `github.com/rudderlabs/analytics-go` dependency, the `RUDDERSTACK_CORSO_WRITE_KEY`/`RUDDERSTACK_CORSO_DATA_PLANE_URL` environment hooks, the `--no-stats` CLI flag, and the `control.Options.DisableMetrics` opt-out. The fork now makes no outbound analytics calls and carries nothing to opt out of. Retired the inherited CI workflows (nightly/longevity/sanity/load test, binary and website publishing, Teams notifications, test-tenant cleanup) that depended on Alcion's secrets, release process and test tenant; a replacement nightly test-tenant job is chunk 0.3's responsibility.
- Added a test-tenant seeding harness: `src/cmd/seed` fills the Microsoft 365 test tenant with known, `k3ep-seed`-prefixed data across Exchange mail/calendar/contacts, OneDrive and a SharePoint document library and list, idempotently (find-by-name, never delete-and-recreate), and writes a machine-readable seed manifest (`src/internal/seed`) that later chunks assert exact backup/restore results against, including each item's real Graph `createdDateTime`/`lastModifiedDateTime` (three lightweight Exchange containers - a mail folder, a calendar, a contacts folder - carry no date at all, because Graph itself returns none for them). Before writing anything, the seeder resolves and reports the target user/site's real name, and after each workload it reports how many items it found already present versus created, so a human re-running it can check the result without trusting a report. Added a `test-integration` target in `src/Makefile` (not part of `make check`) and `.github/workflows/test-tenant.yml`, a manual-trigger-only (`workflow_dispatch`) shape check - the user chose to keep the test tenant's credentials off GitHub entirely, so integration tests against the real tenant run locally, on demand, which satisfies PRD BLD-5's "nightly or on demand" wording even though it deviates (with the user's approval) from the implementation plan's stricter "nightly" row. See `docs/test-tenant-harness.md`.
- Every seed workload's dated manifest items now go through one shared, generic `datedManifestItem` function (`src/internal/seed/dated_item.go`) instead of five separate inline copies of the same ID/date-reading logic, and `dated_item_test.go` mocks all seven Graph types it covers to prove, without touching the tenant, that a found item and a created item always report the same date - a standing regression guard for a property that a live tenant can only ever demonstrate once per item (the harness cannot delete its own seed items to re-create one for a second live proof). Also fixed a narrower reintroduction of the same found-versus-created divergence specifically for OneDrive and SharePoint library files: the file's `ModifiedAt` was read off the metadata-only creation response, before the content upload that follows it, which Graph treats as a further modification; both now re-fetch the item after uploading content. Bumped `ManifestSchemaVersion` to 2 for the `CreatedAt`/`ModifiedAt` type change (`time.Time` to `*time.Time`, so the three dateless container kinds now actually omit the field instead of serialising a zero timestamp); see `src/internal/seed/manifest.go` and `docs/test-tenant-harness.md` for the version history.
- Added one recurring series (`k3ep-seed-event-recurring-001`, five daily occurrences) to the seeded calendar, with one occurrence cancelled and one modified into an exception, specifically to exercise chunk 0.5's Graph SDK upgrade: that upgrade promoted `Event.CancelledOccurrences`/`ExceptionOccurrences` from beta-only `AdditionalData` entries to typed fields and deleted the old map-parsing code, but nothing exercised the migration against real Graph data, since every other seeded event is a single non-recurring occurrence. The cancellation is a `DELETE` on that occurrence's own instance ID (Microsoft's documented mechanism for cancelling a single occurrence, and the only delete anywhere in this package - a legitimate operation on a series this harness created, not a workaround); the exception is a `PATCH` on a different occurrence's instance ID. Both run once, inside the series' own idempotent-upsert create branch. Recorded in the manifest via a new `ManifestItem.Recurrence` field (non-nil only on the series master, `kind: "event_recurring"`) carrying both typed fields' values, plus a new `kind`, `"event_exception"`, giving the one exception occurrence its own top-level manifest item. Bumped `ManifestSchemaVersion` to 3 (purely additive). `smoke_test.go`'s `requireRecurringEventReadsTypedOccurrences` is the live assertion that actually exercises the migration, and fails if either typed field were ever broken or reverted to the old `AdditionalData` path. See `docs/test-tenant-harness.md`'s "The recurring calendar event" section.
- Added `src/pkg/engine`, the deliberate, k3ep-facing public API REPO-2/REPO-3 call for: a thin façade over `pkg/repository` that opens a repository with caller-supplied storage settings and encryption key (`InitializeRepository`/`OpenRepository`), backs up with caller-supplied tags (`Backup`, tenant/policy/object for k3ep) and a progress callback that never writes to stdout, lists and deletes backup points by tag (`ListBackupPoints`/`DeleteBackupPoints` - the latter refuses an empty tag filter outright, so there is no way to delete "everything" through it), and runs maintenance (`RunMaintenance`). Every exported type and function signature uses only plain Go types or other already-public `src/pkg` types, so a caller in a different Go module never needs - and, because Go forbids importing another module's `internal` packages, never can - name a type from `src/internal`. Backing this: a new `store.Tags` general-purpose tag filter (`pkg/store`), and a new `BackupOperation.AdditionalTags` field (`internal/operations`) that merges a caller's tags into a backup's model without ever letting one override the fork's own `service`/`backup-type` bookkeeping tags, even if the caller's key collides with one (`pkg/engine` also rejects a colliding key outright, before any backup work starts, as a clearer caller-facing error).
- Added browse to `src/pkg/engine` (RES-1, RES-2): `ListFolders` returns one backup point's folder tree - Exchange mail folders, calendars and contacts folders, OneDrive folders, SharePoint document library folders and a SharePoint list's own container - and `ListItems` returns the items in one of those folders, or in the whole point, each with a stable ID, a name, a size and its own Microsoft 365 creation and modification dates. `FindItemsByName` finds items by name across the backup points carrying a given tag set (k3ep's object tag), with substring or whole-name matching, case-insensitively; it returns a `FindItemsResult` rather than a bare slice, because a point whose details manifest cannot be read - including one deleted by retention between the call's own listing step and its read - is skipped and named in `UnreadableBackupPointIDs` instead of failing the whole search, and a caller has to be able to tell "nothing matched" from "not everything was searched". All three read only the backup point's own details manifest: no Microsoft Graph call, no item content fetched or decrypted, and no full-text content search, which RES-2 rules out of scope. Item and folder IDs are the fork's own path-derived, opaque, non-reversible entry references, so they identify the same item on a later call and on a later repository connection - the property chunk 5.1's restore and 7.3's erasure both depend on, proven live across two calls and across a `Close`+`OpenRepository` round trip. The durable handle is the **pair** (backup point ID, item ID), not the item ID alone: a drive item's storage path is built from its folder chain's display names, so renaming or moving an ancestor folder gives the same file a different ID in the *next* backup point, while Exchange paths use container IDs and so survive a rename but not a move. An already-listed ID keeps identifying that item in the point it was listed from, because a backup point is immutable.
  - **A backup point that is not independently restorable is refused by default.** `ListBackupPoints` deliberately returns assist and preview points as well as merge points (chunk 0.6, where hiding them turned out to make a tenant's own data undeletable), but an assist point holds only what one interrupted backup managed to upload: its folder tree is a fragment with nothing inside the data saying so, and rendered next to a complete point's tree it looks identical. `ListFolders`/`ListItems` therefore return `ErrIncompleteBackupPoint` for an assist, preview or unclassified point unless the caller sets `AllowIncompletePoint`, and `FindItemsByName` skips such points unless asked. The opt-in exists because GDPR-2 needs it - a future erasure chunk has to enumerate what an incomplete point holds in order to name those items for removal - and when it is used the result still reports `IndependentlyRestorable: false` and the point's type.
  - Browse takes a bare backup point ID and deliberately does **not** check that point's tags, consistent with this package never interpreting tags; establishing that a point belongs to the requesting tenant is k3ep's API layer's job, and the request types now say so, because a point ID is not self-authorising.
  - Names come back in data and never in errors or logs. `Folder.Name`/`Path` and `Item.Name`/`FolderPath` carry real display names, subjects and filenames, because that is what RES-1 and RES-2 are about; every error this code returns identifies a backup point by ID and an entry by its opaque entry ID, never by what it is called, and a test proves it.
  - Two things the real test tenant corrected, rather than being assumed: a SharePoint **document library** is a drive, so it appears as that drive's `root:` folder with the library's name on `DriveName`, not as a folder of its own; and a SharePoint **list** does get a container folder, but one the fork records under the list's own Microsoft 365 ID rather than its title, holding exactly one item - the list itself, whose `ItemCount` is how many rows it captured. A list's rows are not individually browsable or individually addressable, because the fork stores a whole list as a single item. Likewise a recurring calendar series is exactly one browsable item: its exception occurrence is serialised inside the series master and its cancelled occurrence is not returned by Graph at all.
  - Fixed while building it, and caught by this chunk's own test rather than in review: keying the folder tree by each entry's recorded location alone merged folders that genuinely share a location. Every drive's tree starts at an element literally named `root:`, so a site with two document libraries (or any backup point covering both OneDrive and SharePoint libraries) produced several distinct folders whose location is exactly `root:`, and one drive's folders were attached to the other drive's root. Folder identity now includes the service, category and drive ID, which is how the fork's own "unique location" solves the same problem internally before stripping that prefix back off for storage.
  - Added `TestEngineAPI_ExportedSignaturesNameNoInternalType` (`src/pkg/engine/exported_api_test.go`): a mechanical check that no exported `pkg/engine` signature - function, method, exported struct field, interface method, type alias or exported variable - names a type from `src/internal`, including one laundered through an unexported local alias. It extends chunk 0.6's `cmd/backupexample/import_test.go` from the other side: that test proves the *consumer* writes no internal import, while `pkg/engine` legitimately imports internal packages for its implementation, so only this one guards the property k3ep actually relies on. Both are syntactic and both document what they cannot see; Go's own cross-module internal rule remains the real guarantee. `cmd/backupexample` now also exercises browse, so 0.6's import check covers the new API too.
- Added `src/cmd/backupexample`, chunk 0.6's headline proof: it backs up a seeded user's Exchange Inbox with tags and lists the resulting backup point using nothing but `pkg/engine` (and the `pkg/account`/`pkg/storage`/`pkg/selectors`/`pkg/control`/`pkg/credentials` packages needed to build its inputs). `import_test.go` proves mechanically, by parsing the program's own import block, that it never imports an `internal` package - not by inspection, since Go's internal-import restriction does not actually stop a program inside this same module from doing so.

### Removed
- **S3 storage now requires explicit credentials: the CLI's ambient-credential path is gone, deliberately.** `pkg/storage.S3Config.validate()` requires a non-blank (and not whitespace-only) access key and secret key for every S3 configuration, for every caller, with no opt-out anywhere in the fork. The `AllowAmbientCredentials` field chunk 0.6 added, its unconditional setter in `ApplyConfigOverrides`, and every remaining reference are deleted.
  - **What no longer works:** connecting to or initialising an S3 repository with no keys at all, relying on the AWS SDK's own credential chain - an EC2/ECS instance role via IMDS, a shared credentials file or `AWS_PROFILE`, SSO, or web-identity credentials. **What still works unchanged:** supplying an access key and secret key by `--aws-access-key`/`--aws-secret-access-key`, in the Corso config file, or as `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` environment variables, which `ApplyConfigOverrides` reads itself and which are then checked like any other value. Note the distinction, because chunk 0.6's own report entry blurred it: environment *variables* were never the problem; the silent fallback *inside* Kopia's SDK to credentials this fork had never seen was. The user-facing surfaces now say all of this - `corso repo init s3 --help`, `corso repo connect s3 --help`, `corso env`, and `website/docs/setup/repos.md`'s "Credential setup" section, which previously advertised the credentials-file option that no longer works.
  - **Why it was removed rather than fixed.** Chunk 0.6's security review proved live that an S3 config with blank credentials fell through Kopia's AWS SDK chain to the environment and then to the instance role, so a caller-chosen bucket, prefix and endpoint was serviced by the *platform's* identity - 13 requests signed with a credential the caller never supplied. Three successive fixes each moved the problem: guarding `pkg/engine` alone was walkable through the public `pkg/repository`; hardening the shared validator with an opt-in flag left the flag forgeable through the exported `Storage.Config` map; and removing the flag from that map severed the only channel by which it reached its consumers, leaving it vestigial and the CLI's ambient path broken anyway. Dropping the capability is the user's decision and ends that sequence.
  - **The test that let the regression through is replaced, not extended.** `TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials` asserted that a field was set on a struct and never that the capability it controlled still worked, which is why CI stayed green through a real regression to `main`. In its place: `TestS3Config_ApplyConfigOverrides_RequiresExplicitCredentials` (the CLI's own path cannot produce a usable `Storage` without credentials, and still can with them), `TestS3Config_Validate_RequiresCredentials_NoOptOut` (missing, empty, whitespace-only and tab-only access and secret keys; the error names which field is missing and never echoes the value, a prefix of it, or its length), `TestS3Config_HasNoCredentialOptOutField` (a reflective guard against the field being re-added), and `TestBuildS3ConfigFromMap_NoMapKeyOptsOutOfCredentials` (the eight forged map-key spellings 0.6's security reviewer used).
  - **The security reviewer's attack is now reproduced in-repo, measured from the endpoint's own side.** `pkg/repository/s3_credentials_test.go` puts fake credentials in the process environment, points a credential-less config map - carrying each forged key spelling in turn - at a loopback HTTP recorder, and drives it through `pkg/repository.New`+`Initialize`, the connect path's config-filename hash, and `UpdatePassword`: zero requests, no `Authorization` header. `internal/kopia/s3_credentials_test.go` does the same at the consumer, `s3BlobStorage`, with a positive control proving the recorder does see a signed request when credentials are explicit. Both reproduce the original attack when the credential requirement is removed (9 and 5 signed requests respectively) and when an opt-out is re-added (1 request each, only for the spelling the re-addition honours).
  - **No config hash moved**, measured at the base commit and at this head rather than inherited, because the repository's config filename derives from them and a shift would strand every existing repository. `TestGetStorageConfigHash_PinnedValues` now pins them by exact value. It also pins something easy to get wrong: the hash is **credential-sensitive**, because `excludedS3ConfigFieldsForHashing` filters by top-level field name and the embedded `credentials.AWS` is one field named `AWS`, so its `AccessKey`/`SecretKey`/`SessionToken` entries never match anything. That is pre-existing and untouched here; it is pinned so that anyone "fixing" the exclusion list sees the filename consequence first.
### Changed
- Upgraded the Microsoft Graph SDK (`github.com/microsoftgraph/msgraph-sdk-go` `v1.30.0` → `v1.104.0`, `msgraph-sdk-go-core` `v1.0.1` → `v1.4.1`), Kiota (`kiota-abstractions-go` `v1.5.4` → `v1.11.2`, `kiota-authentication-azure-go` `v1.0.1` → `v1.3.1`, `kiota-http-go` `v1.1.1` → `v1.5.8`, `kiota-serialization-form-go` `v1.0.0` → `v1.1.4`, `kiota-serialization-json-go` `v1.0.5` → `v1.1.5`, `kiota-serialization-text-go` `v1.0.0` → `v1.1.4`, `kiota-serialization-multipart-go` `v1.0.0` → `v1.1.2`), `azidentity` (`v1.13.1` → `v1.15.0`) and MSAL (`github.com/AzureAD/microsoft-authentication-library-for-go`, `v1.7.2` → `v1.10.1`; already moved off `v1.2.1` by chunk 0.4's Kopia bump, so `v1.7.2` is this chunk's true starting point). Also bumped `golang.org/x/net`, `golang.org/x/tools` and `github.com/stretchr/testify`, forced by the above via minimum-version-selection; two new transitive indirect dependencies appeared (`github.com/go-ole/go-ole`, `github.com/mxk/go-vss`), pulled in by `azidentity`'s Windows credential-cache support and unused on the platforms this fork actually runs on.
  - The betasdk vendored under `src/pkg/services/m365/api/graph/betasdk/models` used `models.Jsonable`/`models.CreateJsonFromDiscriminatorValue` for untyped JSON values (`WebPartData.Properties`, `MetaDataKeyValuePair.Value`); the current SDK removed `models.Json` entirely in favor of `kiota-abstractions-go/serialization`'s `UntypedNodeable`/`CreateUntypedNodeFromDiscriminatorValue`, so both betasdk types were updated to the new type.
  - The Graph SDK's `Event` model gained typed `CancelledOccurrences ([]string)` and `ExceptionOccurrences ([]Eventable)` fields, promoting what used to be beta-only properties that landed only in `AdditionalData` when fetched through the stable SDK against the beta endpoint. Every place that read `event.GetAdditionalData()["cancelledOccurrences"/"exceptionOccurrences"]` and hand-parsed the result (`pkg/services/m365/api/events.go`'s `GetCancelledEventDateStrings` and `fixupExceptionOccurrences`, `internal/m365/collection/exchange/events_instance_restore.go`'s recurring-event restore path, `internal/converters/ics/ics.go`'s `FromEventable`) now reads the typed fields directly, which is simpler and removes several defensive type assertions that existed only because the old shape was untyped `any`. `toEventSimplified` (`internal/m365/collection/exchange/transform.go`) now clears both typed fields explicitly instead of deleting map keys that no longer exist. The now-dead `api.EventFromMap` and `parseableToMap` helpers, which existed only to round-trip these values through JSON, were removed.
  - `drives.ItemItemsItemInviteResponseable` (the response type of `Drives().ByDriveId(...).Items().ByDriveItemId(...).Invite().Post(...)`, used by `pkg/services/m365/api/drive.go`'s `PostItemPermissionUpdate` and its two callers' handler interfaces) is now deprecated in favor of `ItemItemsItemInvitePostResponseable`, returned by the SDK's new `PostAsInvitePostResponse(...)` method; both are identical in shape (the deprecated interface now simply embeds the new one). Switched to the new type and method.
- Also bumped `golang.org/x/tools`, `github.com/Azure/azure-sdk-for-go/sdk/azidentity`, `github.com/AzureAD/microsoft-authentication-library-for-go` (MSAL, `v1.2.1` → `v1.7.2`, an indirect dependency of `azidentity`), `github.com/minio/minio-go/v7`, `github.com/golang-jwt/jwt/v5`, `github.com/spf13/cobra`/`pflag`, `github.com/stretchr/testify`, `go.uber.org/zap`, `golang.org/x/net`/`x/time`/`x/exp` as an unavoidable side effect of Go's minimum-version-selection: current upstream Kopia's own `go.mod` requires newer floors for all of these, so they move regardless of what this fork's `go.mod` asks for. This includes MSAL, even though it is named in this chunk's out-of-scope list alongside the Graph SDK, Kiota and azidentity: Kopia's own `go.mod` requires a newer `azidentity`, which itself requires a newer MSAL, so the bump happens whether or not this chunk asks for it. The Microsoft Graph SDK's and Kiota's own direct requires are genuinely untouched; that upgrade is a separate, later chunk, which should plan its work assuming MSAL has already moved to `v1.7.2`, not `v1.2.1`. Fixed the resulting fallout: two deprecated `pflag.FlagSet.ParseErrorsWhitelist` call sites renamed to `ParseErrorsAllowlist`, one test's `golang.org/x/net/context` import swapped for the stdlib `context` package (fixing a previously-undetected discarded-cancel-function leak the swap exposed), and two unit test fixtures' placeholder JWT signature segments changed from the literal word `"signature"` to a validly base64url-encoded placeholder, since `golang-jwt/jwt/v5` v5.3.1 (unlike v5.2.0) now base64-decodes the signature segment even when parsing unverified.
- Removed the `replace github.com/xhit/go-simple-mail/v2 => github.com/alcionai/go-simple-mail/v2` directive. The fork existed to add `Email.UseProvidedAddress`, a toggle to skip RFC 5322 address validation/parsing, because Corso sometimes has incomplete email addresses it still needs to convert (`internal/converters/eml`). That exact feature (PR xhit/go-simple-mail#97, "Add a toggle to skip email validation") has since been merged into the real upstream `github.com/xhit/go-simple-mail/v2` repository's default branch, but upstream has not cut a new tagged release since `v2.16.0` (which predates that merge) to include it. `src/go.mod` now requires the canonical module directly at the pseudo-version of the upstream commit that contains the feature (`v2.16.1-0.20261016114315-0069ecddfb3c`) instead of the Alcion fork, with no `replace`. Every field and method `internal/converters/eml` uses (`UseProvidedAddress`, `AllowDuplicateAddress`, `AddBccToHeader`, `AllowEmptyAttachments`, `Encoding`, `mail.File`, etc.) is unchanged between the fork and this upstream commit; all existing `eml` tests pass unmodified.

### Fixed
- `pkg/engine.ErrEmptyTagFilter`'s message named only one of its two callers: it read "engine: DeleteBackupPoints requires at least one tag" although chunk 0.7 made `FindItemsByName` return the same error, so a caller reading a log line - the only evidence that call leaves behind - was told about a function it had not called. It now reads "engine: this operation requires at least one tag". The error's identity is unchanged; `errors.Is` still recognises it from both callers, and `TestErrEmptyTagFilter_MessageNamesNoSingleCaller` fails if a future message names one caller without the other.
- Fixed a pre-existing data race in `pkg/services/m365/api/pagers`' `GetAddedAndRemovedItemIDs`, confirmed on clean `main` with `go test -race ./pkg/services/m365/api/pagers/...` and independently by chunk 0.5's reviewer: `batchWithMaxItemCount`'s ctx-cancelled and `getAddedAndRemoved`-error early-return paths called `Cancel()` (or, for the ctx-cancelled path, nothing at all) but never waited on `Results()`, so the function - and, through it, the caller reading the pager's own `ValidModTimes()` immediately afterwards - could return while the background enumeration goroutine was still concurrently calling `GetPage()` on that same pager. Both paths now call `Cancel()` and wait on `Results()` before returning, discarding its own return value (the specific ctx or `getAddedAndRemoved` error still wins), purely for the synchronization: a channel close happens-before the receive that observes it, so waiting here guarantees the goroutine has genuinely stopped touching the pager. Verified clean with `go test -race -count=8`.
- Fixed two further bugs chunk 0.6's live test-tenant run against `pkg/engine` surfaced, both pre-existing in `pkg/repository`/`internal/m365` but never hit before because the CLI always opens a repository already knowing which service it will use, where `pkg/engine` deliberately does not:
  - **Load-bearing**: `internal/m365.Controller` derived its resource lookup handler (Users/Groups/Sites) once, from whichever service first connected the data provider, and never re-derived it when the same controller was reused for a later call naming a different service - including `path.UnknownService`, which `pkg/engine`'s `InitializeRepository`/`OpenRepository` pass before any `Backup` call has chosen a real one. Every real backup through `pkg/engine` panicked on a nil handler the first time; reverting `Controller.EnsureResourceHandler` (added here, called from `pkg/repository`'s `connectToM365` on every reuse, not just first creation) reproduces the panic.
  - **Not load-bearing, but makes `Repository.ID()` meaningful**: `pkg/repository` never actually persisted a freshly initialized repository's generated ID into its own model store - `newRepoModel` was defined but, other than from a unit test, never called - so reconnecting with the fork's own `events.RepoIDNotFound` sentinel (which asks for the real ID back rather than minting a new one) always read back an empty ID. Every other `pkg/engine` operation passes live without this fix; only identity across a `Close`+`OpenRepository` round trip depended on it. `setupKopia`'s Initialize path now persists the ID on a genuine first create, and adopts the already-persisted ID instead of minting a conflicting second one when Initialize's own documented reconnect-on-matching-passphrase behavior kicks in.

  Both confirmed fixed by re-running `pkg/engine`'s live integration suite (`internal/seed/engine_integration_test.go`, `make test-integration`, standalone and combined) and `src/cmd/backupexample` against the real test tenant, for both the filesystem and a local MinIO backend.
- Fixed six further `pkg/engine` findings from chunk 0.6's standard and security reviews (first fix round):
  - **[HIGH]** `pkg/repository.BackupsByTag` silently drops any backup tagged `model.AssistBackup`. A single recoverable error during an otherwise-real backup is enough to produce one (`createBackupModels`/`isAssistBackup`), and `pkg/engine.Backup` itself returns an error and no `BackupPoint` in that case - but `doPersistence` had already written the snapshot, the details streamstore entry and this very model, carrying the caller's tags, before the error surfaced. That content was then listable and deletable nowhere. Added `pkg/repository.AllBackupsByTag` (and its testable core, `allBackupsByTag`), which does not filter; `pkg/engine`'s `ListBackupPoints`/`DeleteBackupPoints` now call it instead of `BackupsByTag`, and their doc comments state the resulting, deliberate semantics - every backup point is visible and deletable by tag, assist and preview bases included - for chunk 0.7 to build browse on top of correctly. `DeleteBackupPoints` now returns the deleted points' own IDs, not just a count, so k3ep's AUD-1 audit event does not need a separate, racy list-then-delete round trip.
  - **[HIGH]** An S3 `Storage` with no caller-supplied access/secret key fell through to Kopia's AWS SDK's own ambient-credential chain (environment variables, then IMDS), reaching a caller-chosen bucket, prefix and endpoint using the *platform's* identity rather than the caller's - proven live. Added `pkg/engine.validateStorageCredentials`, enforced in `pkg/engine` only, before any network call: `pkg/storage.S3Config`'s own shared validator is untouched, since the CLI legitimately relies on ambient credentials.
  - **[MEDIUM]** `validateTags` only rejected the two tag keys the fork's own backup-type logic uses. Kopia's `ModelStore.Put` independently rejects `"type"`/`"stableID"`/`"storeVersion"` outright, but only after a real backup has already uploaded a snapshot and written its details - leaving both orphaned until a full maintenance pass's GC buffer expires. Per a user decision this round, also reserved `"tenant"` (exact match) and the `"sc-"` prefix, which the fork's own backup garbage collector (`internal/kopia/cleanup_backups.go`) reads and writes to decide which assist bases survive collection - a caller tag using either made this package's "never interprets tags" promise false. k3ep's own tags moved to `k3ep-tenant`/`k3ep-policy`/`k3ep-object` accordingly. Also bounded tag count (32) and key/value size (256/1024 bytes): Kopia's manifest manager holds every manifest's labels in memory per `Find` call, so 1000 tags of 64KB each (previously accepted) was a real, multiplied memory cost.
  - **[MEDIUM]** This chunk's own `Controller.EnsureResourceHandler` (added to fix an earlier bug, see above) wrote `Controller.resourceHandler` with no synchronization, while `PopulateProtectedResourceIDAndName` read it - a confirmed data race under two concurrent `Backup` calls sharing one `*engine.Repository`, and so one underlying `*m365.Controller`, which both reviewers found independently. Also fixed: an unrecognized service left a non-nil interface wrapping a nil `*resourceGetter` (a classic Go typed-nil), so the nil guard never fired and the failure was a panic instead of the documented `ErrNoResourceLookup`. `setResourceHandler` now declares its local variable with the interface type, not the concrete pointer type, and every read/write of `resourceHandler`/`IDNameLookup` is guarded by the Controller's existing mutex.
  - **[MEDIUM]** `RepositoryConfig` embeds `Storage.Config`/`Account.Config`, plain `map[string]string` fields holding the repository's own encryption key and the Microsoft 365 application's client secret, with no protection from `fmt`'s default verbs (`%v`, `%+v`, `%#v`, `%s` all printed them in full) or from `log/slog`. Added `String`/`GoString`/`LogValue` methods that redact both maps, and documented `RepositoryConfig`'s trust model (this package enforces almost nothing about where `Storage` points or what `Account` authenticates as; deriving both safely, server-side, is k3ep's obligation) in its own doc comment.
  - **[MEDIUM, standard review]** `pkg/engine` permanently disabled the Graph concurrency limiter for the whole process: `InitializeRepository`/`OpenRepository` connect a data provider for `path.UnknownService` (no backup has chosen a real service yet), and `m365.NewController`'s own `graph.InitializeConcurrencyLimiter(ctx, service == path.ExchangeService, ...)` call is a `sync.Once` - so the first repository ever opened through `pkg/engine` permanently latched the limiter off for every Exchange backup the process would ever run afterwards, k3ep's long-lived-worker shape included. Added `pkg/engine.ensureConcurrencyLimiterInitialized`, called with `enable=true` before `repository.New` can let `m365.NewController` win that race with the wrong value.

  Every fix proven by a test that fails without it (reverted, confirmed failing, restored): `pkg/repository`'s `TestAllBackupsByTag_IncludesAssistBackups`, `pkg/engine`'s `TestListBackupPoints_IncludesAssistBackups`/`TestDeleteBackupPoints_DeletesAssistBackupsAndReturnsIDs`, `TestValidateStorageCredentials`, `TestValidateTags_RejectsReservedKeys`/`TestValidateTags_BoundsTagCountAndSize`, `internal/m365`'s `TestEnsureResourceHandler_ConcurrentWithPopulate`/`TestSetResourceHandler_UnrecognizedServiceIsNilInterface` (the latter reproducing the exact live panic), and `pkg/engine`'s `TestRepositoryConfig_NeverLeaksCredentials`. The concurrency-limiter fix is proven by `internal/seed/engine_integration_test.go`'s `TestEngineIntegrationSuite` passing standalone (`go test -run TestEngineIntegrationSuite ./internal/seed/...`), which it did not before this round (see below).
  - Also fixed, standard review: `TestEngineIntegrationSuite` only passed as part of the whole `./internal/seed/...` suite, because an earlier-running test file's own `graph.InitializeConcurrencyLimiter` call happened to win the same `sync.Once` race described above; run alone, it failed with "nil concurrency limiter". Added the identical call (`api_integration_test.go`/`smoke_test.go`'s own pattern) to all three `EngineIntegrationSuite` test methods, and re-verified the whole suite passes both standalone and combined, with and without a MinIO container.
  - `src/cmd/backupexample` **[LOW]** defaulted to a hardcoded repository passphrase when `BACKUPEXAMPLE_PASSPHRASE` was unset, and let `KopiaCfgDir` default to a predictable `/tmp/repository-<hash>.config` - example code is what gets copied. The passphrase is now required with no fallback; `KopiaCfgDir` is set explicitly under the program's own temp directory.
  - `internal/seed/engine_integration_test.go` took `t := suite.T()` once at the top of `TestBackup_TagsProgressDeleteMaintenance` and reused it inside every `suite.Run(...)` subtest closure, so a subtest's own failures were attributed to the parent test and the subtest itself always printed PASS regardless. Each subtest now takes its own `t := suite.T()` first, as every other suite in this fork already does.
  - `src/cmd/backupexample/import_test.go`'s own doc comment now records exactly what its mechanical "no internal import" check does not catch (a same-module value reached through Go's type-inference escape hatch rather than an explicit import, a subdirectory package, a `_test.go` helper, a build-tag-gated file), now that the pattern is a repo-wide convention (see the fork's `CLAUDE.md`).
- Fixed eleven further `pkg/engine` findings from chunk 0.6's second standard and security review round, the last authorised for this chunk:
  - **`json.Marshal` still leaked both secrets even after the first round's redaction fix.** `fmt`, `zap`, `slog` and `clues` all route through `Stringer`/`LogValuer`, but `encoding/json` consults neither, so `json.Marshal(cfg)` (and any struct embedding `RepositoryConfig`, now a real risk since chunk 1.4's job queue persists payloads to Postgres) still emitted the repository passphrase and the M365 client secret verbatim; `cfg.Storage`/`cfg.Account` were also exported with no redaction under any channel standalone. Added `MarshalJSON` to `RepositoryConfig`, and `String`/`GoString`/`MarshalJSON` to `storage.Storage` and `account.Account` directly (`pkg/storage/redact.go`, `pkg/account/redact.go`), so a caller of either package alone - not just through `pkg/engine` - gets the same protection. Confirmed nothing in the fork marshals either type wholesale outside a filtered config-hash map; the CLI and config file are unaffected.
  - **Widened `TestRepositoryConfig_NeverLeaksCredentials`**, which only covered `fmt` verbs and `slog` and so would have passed even with the `json.Marshal` leak above. Rewrote it (`pkg/engine/redact_test.go`) into subtests covering `fmt`, `json` (the config directly, embedded in another struct, and `Storage`/`Account` alone), `slog` (a real `slog.NewJSONHandler`), `zap` (a real `zapcore` JSON encoder) and `clues` (a real `clues.NewWC(...).With(...)`, checked via `String()`, `%v` and `json.Marshal` of the resulting core) - plus a sanity subtest proving the raw, unredacted struct really does carry the secret, so the other subtests cannot be vacuously passing against an already-empty value.
  - **The Controller mutex was partial and its own doc comment overclaimed what it covered.** `IDNameLookup` was written under `ctrl.mu` but read unguarded on the backup hot path (`internal/m365/backup.go`'s `ProduceBackupCollections`, reading `ctrl.IDNameLookup.IDs()`), confirmed racing under `-race` with both sides being this fork's own code - two concurrent `Backup` calls sharing one `*engine.Repository`, and so one `*m365.Controller`, is exactly k3ep's shape. Fixed by removing the shared read entirely rather than locking it: `ProduceBackupCollections` now builds its own local ID list from `BackupProducerConfig.ProtectedResource` (the same resource `PopulateProtectedResourceIDAndName` already set from, confirmed by tracing `internal/operations/backup.go`'s `doBackupProduce`), so there is no shared mutable state left to race on. Also locked `Wait()` (reads and resets `status`), `Status()` and `PrintableStatus()` to match `UpdateStatus`'s existing locked write, and narrowed the struct's doc comment to name exactly the fields `mu` covers. Added `internal/m365/controller_race_test.go`'s `TestControllerConcurrency_ProduceBackupCollectionsAndPopulate`, pairing the real `ProduceBackupCollections` against the real `PopulateProtectedResourceIDAndName` concurrently; reverting the fix and re-running under `-race -count=10` reproduced the race every time, confirming the test has teeth.
  - **Whitespace-only S3 credentials passed `validateStorageCredentials`.** `" "` and `"\t"` have `len > 0`, so they reached Kopia's own S3 client and cost a round trip of signed, failing requests against the caller-named endpoint before surfacing a confusing error, instead of being rejected synchronously up front like a genuinely empty credential. `validateStorageCredentials` now rejects a credential that is empty after `strings.TrimSpace`.
  - **Tag bounds were per-tag only, not per-map** - see the dedicated commit and entry above (review round 3 fix 5): added `maxTagTotalBytes` (4096) as a bound on the whole tag map's combined key and value bytes, independent of the existing per-key (256B)/per-value (1024B)/count (32) bounds, which alone let roughly 41 KB of Kopia manifest labels through on a single backup point.
  - **`operations.mergeAdditionalTags`'s doc comment overstated its own backstop.** It claimed to "defensively" enforce the full reserved-key rule, but only ever refused to overwrite a key already present in the tag map before merging - which covers `service`/`backup-type` (set by `createBackupModels` before merge runs) but not `type`/`stableID`/`storeVersion`/`tenant`/`sc-*` (never pre-populated), so an in-fork caller of `BackupOperation.AdditionalTags` bypassing `pkg/engine`'s own check could still forge any of those five. Widened `reservedAdditionalTagKeys` and the prefix check so `mergeAdditionalTags` unconditionally drops all of them, present or not, matching the comment's original claim instead of narrowing the comment to fit the weaker behavior.
  - **`pkg/repository.AllBackupsByTag`'s doc comment was wrong about preview backups.** It claimed to include "assist and preview bases that `BackupsByTag` hides"; `backupsByTag` only ever filtered `model.AssistBackup` - preview backups were never hidden by it. Corrected the comment to say so.
  - **`ensureConcurrencyLimiterInitialized`'s doc comment called the forced limiter "harmless"/"inert".** It is a process-global semaphore clamped to `[1,4]` in-flight Graph requests, covering every service and shared across every tenant and concurrent backup in the process, and the first repository ever opened through `pkg/engine` - including one that goes on to fail authentication - permanently latches it for the rest of the process's life via Kopia's own `sync.Once`. Measured instead of asserted: ~400ms for 24 concurrent `users` calls with the limiter forced off, ~745ms at `ItemFetch: 4`, ~1.647s at `ItemFetch: 1`. Rewrote the comment to state this plainly. Per an explicit decision this round, the underlying `sync.Once`/package-singleton design itself is intentionally not touched here: the user approved a separate chunk for a per-client limiter (Phase 2, before the worker fan-out that makes the shared cap bite hardest).
  - **Reversing a round-1 instruction: added `S3Config.AllowAmbientCredentials` and made the shared validator fail closed by default.** The security review proved `pkg/engine`'s own `validateStorageCredentials` was not the only path to a repository: `pkg/repository.New`+`Initialize` with blank S3 credentials still reached Kopia's AWS SDK's ambient-credential chain directly, and `pkg/repository` lives under `src/pkg`, so REPO-2 lets k3ep import it regardless of `CLAUDE.md`'s prose steering callers to `pkg/engine`. Added an explicit `AllowAmbientCredentials bool` field to `S3Config`, defaulting false; `validate()` now requires non-blank access and secret keys unless it is set. It is set to `true` only inside `ApplyConfigOverrides`, reached exclusively from the CLI's own config-loading path (`cli/repo/*.go` via `pkg/config.ReadCorsoConfig`) and never from `pkg/engine`, so the CLI keeps working with environment or IAM credentials while every other caller fails closed by default. Fixing this exposed a latent, previously-harmless bug: `normalize()` never copied `AWS`/`AllowAmbientCredentials` into its returned copy, so `StringConfig()`'s own `validate()` call was always checking blank credentials regardless of the real config - fixed alongside, since the new stricter check would otherwise reject every S3 config unconditionally. `storage.NewStorage` itself now refuses to construct a `Storage` with blank S3 credentials (unless `AllowAmbientCredentials` is set), which in turn required updating four existing tests that built credential-indifferent S3 fixtures without realistic-looking credentials (`pkg/storage/s3_test.go`, `pkg/storage/storage_test.go`, `pkg/engine/storage_validation_test.go` rewritten to build raw `storage.Storage{Config: ...}` literals where a bad-credential fixture is still needed) and a CLI test mock (`cli/utils/testdata/opts.go`) that did not yet implement an interface method added in round 1. `make check` stays green; the CLI's own config-driven path is covered by a new `TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials`.
  - **`BackupPoint` had no typed way to tell a merge point from an assist or preview one.** Outside this module, a caller cannot name `model.AssistBackup` (an out-of-module import of an `internal` package fails to compile), so without a typed field k3ep would have had to hardcode the raw tag literals `"backup-type"`/`"assist-backup"` - against a key `pkg/engine` simultaneously reserves from callers - and would miss `pkg/backup.Backup.Type()`'s own version-aware default for pre-v8 backups with no tag at all. Added `BackupPointType` (`BackupPointTypeMerge`/`Assist`/`Preview`/`Unknown`) and set it in `toBackupPoint` from the public `b.Type()`, keeping the raw tag on the struct too for transparency. Documented that a listed point is not necessarily independently restorable (an assist or preview base). Added `TestBackupPointTypeFrom` (6 cases, including the pre-v8 default and an unrecognized value) and a live assertion in `internal/seed/engine_integration_test.go` that a real backup with no injected failure comes back `BackupPointTypeMerge`.
  - **Closed the one untested link in round 1's assist-visibility fix.** Both of round 1's own proofs (`pkg/engine`'s `TestListBackupPoints_IncludesAssistBackups` and `internal/operations`'s `TestMergeAdditionalTags`) replicated the `model.AssistBackup` tag by hand rather than exercising the logic that actually produces it, so nobody had proven a live partial failure really gets classified `assist-backup` rather than silently stopping or classifying some other way. Extracted `createBackupModels`' inline classification switch verbatim into its own `classifyBackupType` function and added `internal/operations/classify_backup_type_test.go`'s `TestClassifyBackupType`, covering every branch with real `fault.Bus` fixtures (`fault.New(false)`, `.AddRecoverable`, `.Fail`) rather than a hand-built tag map: a clean run (merge), one recoverable error under `FailAfterRecovery` with new details (assist - the live scenario round 1 fix 1 is about), the same error under `BestEffort` (still merge, by design), a hard failure, and preview mode both succeeding and failing its own merge-equivalent check. The full pre-existing `internal/operations` suite passed unchanged after the extraction, confirming it is behavior-preserving.

  Every fix proven by a test that fails without it, reverted and restored (same method as round 1): `pkg/engine/redact_test.go` (rewritten), `pkg/storage/redact_test.go`'s `TestStorage_NeverLeaksConfig`, `pkg/account/redact_test.go`'s `TestAccount_NeverLeaksConfig`, `internal/m365/controller_race_test.go`'s `TestControllerConcurrency_ProduceBackupCollectionsAndPopulate` (10/10 reproductions under `-race` with the fix reverted), `pkg/engine/storage_validation_test.go`'s whitespace cases, `pkg/engine/tags_test.go`'s new total-bytes cases, `internal/operations/merge_additional_tags_test.go`'s widened reserved-key case, `pkg/repository`'s corrected `AllBackupsByTag` doc comment (verified by its existing `TestAllBackupsByTag_IncludesAssistBackups`, unchanged behavior), `pkg/storage/s3_test.go`'s new `TestS3Config_ApplyConfigOverrides_CLIPathAllowsAmbientCredentials`/`TestS3Config_Validate_RejectsBlankCredentialsByDefault`, `pkg/engine/internal_test.go`'s `TestBackupPointTypeFrom`, and `internal/operations/classify_backup_type_test.go`'s `TestClassifyBackupType`. Re-ran the full `make check`, `-race -count=5` on every touched package, and the live test-tenant suite (`internal/seed/engine_integration_test.go`) both standalone and combined, with a fresh standalone MinIO container, torn down afterwards.
- Fixed four further `pkg/engine`/`pkg/storage`/`internal/m365` findings from chunk 0.6's fourth and final review round, a direct consequence of round 3's own fixes:
  - **`AllowAmbientCredentials` round-tripped through the exported `Storage.Config` map.** `StringConfig` wrote `s3_allow_ambient_credentials` and `buildS3ConfigFromMap` read it back, so a hand-built or rehydrated map carrying the flag set and no credentials passed `ToS3Config`/`validate` - proven live, through `pkg/repository.Initialize` directly, signing 13 requests with the platform's ambient AWS identity. `AllowAmbientCredentials` is now a Go field only, never serialised to or read from the map; the only way to set it is to hold a `*S3Config` directly (`ApplyConfigOverrides`, the CLI's own config path, unaffected). Added `pkg/storage/s3_test.go`'s `TestBuildS3ConfigFromMap_IgnoresAllowAmbientCredentialsKey`, proving the exact forged map still fails validation. Confirmed live against a fresh standalone MinIO container: the identical forged map through `pkg/repository.Initialize` now fails at storage validation with the expected error, and the real CLI (`go run . repo init s3`, no explicit S3 credentials, only `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` in the environment) still successfully initializes a repository on MinIO. Also confirmed, per the orchestrator's specific request, that no config hash moved: `configHash()` already excludes this field by name via reflection regardless of map serialization, so removing it from `StringConfig` cannot change any hash - verified by re-running both packages' `TestGetAccountConfigHash` unchanged.
  - **`BackupPointTypeUnknown`'s doc comment was inverted.** It claimed the empty string `pkg/backup.Backup.Type()` returns is documented "for some pre-v8 backups"; in fact `Type()` returns empty for a missing tag with a zero (`version.NoBackup`) or post-v8 `Version` - a genuinely pre-v8 backup with no tag defaults to merge, not unknown. Corrected the comment.
  - **The version-aware merge default was trusted even for an unset `Version`.** An untagged model with `Version == 0` (never a genuine pre-v8 backup, only one some other tool or a legacy/partial write left unset) satisfied `Type()`'s own fallback condition (`Version != NoBackup(-1) && Version <= v8's migration point`) and classified `BackupPointTypeMerge` - the one type this package documents as independently restorable. `backupPointTypeFrom` now additionally requires a real, positive `Version` before trusting that default for an untagged model; an unset or negative-but-not-`NoBackup` `Version` with no tag is `BackupPointTypeUnknown` instead. Not reachable through `engine.Backup` today, but reachable for a model written elsewhere, and a future restore chunk will branch on this field. New `TestBackupPointTypeFrom` cases for zero and negative non-`NoBackup` versions.
  - **`controller.go`'s doc comment over-claimed what its own race test proves.** It called `TestControllerConcurrency_ProduceBackupCollectionsAndPopulate` a permanent regression test "for the write side"; measured, with no concurrent reader of `IDNameLookup` left in this fork, removing the mutex from `PopulateProtectedResourceIDAndName`'s write alone would not be caught by `-race` - what the test actually catches is `ProduceBackupCollections` (or anything else) reintroducing the unguarded read. Corrected the comment.
  - Optional, accepted: added `BackupPointType.IsIndependentlyRestorable()`, a single typed predicate (true only for `BackupPointTypeMerge`) so a later browse/restore chunk never has to re-derive "only merge is restorable" from this type's prose doc comments. `TestBackupPointType_IsIndependentlyRestorable` covers every defined value, the zero value, and an unrecognized future value.

  Every fix proven the same way: `pkg/storage/s3_test.go`'s new test (reverted the map-read removal, confirmed the forged map validated and `AllowAmbientCredentials` read back true, restored byte-identical via `diff`); `pkg/engine/internal_test.go`'s two new `TestBackupPointTypeFrom` cases (reverted the version guard, confirmed both failed with a false "merge" result, restored byte-identical). Re-ran the full `make check`, `-race -count=5` on every touched package, and the live test-tenant suite both standalone and combined, with a fresh standalone MinIO container, torn down afterwards; separately re-ran the real CLI against that MinIO container with only ambient environment credentials, and the security reviewer's exact forged-map attack against the real `pkg/repository.Initialize` path, confirming both now behave as expected.
- Fixed the test-tenant seeding harness's SharePoint list item idempotency, which a live run against the real test tenant proved was completely broken: Kiota deserialises a list item's `Fields.AdditionalData["Title"]` as a `*string`, not a `string`, so the "is this title already seeded" type assertion always failed and every seeder run created a fresh duplicate list item. Also removed a read-after-write race in the same code path (querying for a just-created item's ID immediately after creation, rather than reading it off the creation response). Found, during the same work, a pre-existing bug in `pkg/services/m365/api/sites.go`'s `Sites.GetByID`/`GetIDAndName`: its dispatch regex requires at least two comma-separated UUIDs (the full `hostname,siteCollectionId,webId` form) and misclassifies a bare site-collection GUID as a URL, sending Graph a nonsense hostname. Not fixed in `sites.go` itself (a broader change with other callers to consider); the seeding harness works around it locally by reaching the same `Sites().BySiteId(...)` call its other SharePoint code already uses successfully with that exact ID shape.
- Fixed the `Sites.GetByID`/`GetIDAndName` site-ID dispatch bug recorded above: `siteIDRE` required at least two comma-separated UUIDs (the full `hostname,siteCollectionId,webId` composite form), so a bare site-collection GUID with no comma at all fell through to the webURL-parsing branch and Graph was sent a nonsense hostname. Replaced the regex with `isSiteID`, which recognizes all three valid forms Graph's `/sites/{id}` accepts: a bare site-collection GUID, a `siteCollectionId,webId` pair, and the full `hostname,siteCollectionId,webId` composite. Covered by a new unit test (`pkg/services/m365/api/sites_test.go`'s `TestIsSiteID`) exercising the bare-GUID, composite and URL forms, plus a strengthened integration test case in the pre-existing `SitesIntgSuite.TestSites_GetByID` (targets a different, Alcion-owned test tenant and was not run as part of this chunk) that now asserts a bare, nonexistent site ID returns `core.ErrNotFound` specifically rather than "some error", which only a correctly-dispatched request can produce. `internal/seed/run.go`'s local workaround for this bug is no longer needed but was left in place, with its comment updated to say so, since `internal/seed` is owned by chunk 0.3.
- Removed an attempted re-implementation of the Alcion Kopia fork's 60-second S3 TLS handshake timeout that briefly swapped the process-wide `http.DefaultTransport` for the duration of opening an S3-backed repository connection. That swap was a genuine data race (an unsynchronized concurrent read/write of an interface variable, not merely a loosened timeout): `src/pkg/services/m365/api/graph/http_wrapper.go`'s `defaultTransport()` reads the same global with no shared lock whenever Corso builds a Graph HTTP client, and a worker making Graph calls while opening an S3 repository -- the normal case in k3ep -- would race on it. Confirmed under `go run -race`. S3 TLS handshakes now time out after Go's default 10 seconds rather than the fork's 60; see `src/internal/kopia/s3.go` for the full reasoning and the upstream-contribution path if a real destination is ever found to need longer.
- Handle the case where an email or event cannot be retrieved from Exchange due to an `ErrorCorruptData` error. Corso will skip over the item but report it in the backup summary.
- Emails attached within other emails are now correctly exported
- Gracefully handle email and post attachments without name when exporting to eml
- Use correct timezone for event start and end times in Exchange exports (helps fix issues in relative recurrence patterns)
- Fixed an issue causing exports dealing with calendar data to have high memory usage

## [v0.19.0] (beta) - 2024-02-06

### Added
- Events can now be exported from Exchange backups as .ics files.
- Update repo init configuration to reduce the total number of GET requests sent
  to the object store when using corso. This affects repos that have many
  backups created in them per day the most.
- Feature Preview: Corso now supports backup, export & restore of SharePoint lists. Lists backup can be initiated using `corso backup create sharepoint --site <site-url> --data lists`.
- Group mailbox(aka conversations) backup and export support is now officially available. Group mailbox posts can be exported as `.eml` files.

### Fixed
- Retry transient 400 "invalidRequest" errors during onedrive & sharepoint backup.
- Backup attachments associated with group mailbox items.
- Groups and Teams backups no longer fail when a resource has no display name.
- Contacts in-place restore failed if the restore destination was empty.
- Link shares with external users are now backed up and restored as expected
- Ensure persistent repo config is populated on repo init if repo init failed partway through during the previous init attempt.

### Changed
- When running `backup details` on an empty backup returns a more helpful error message.
- Backup List additionally shows the data category for each backup.
- Remove hidden `--succeed-if-exists` flag for repo init. Repo init will now succeed without error if run on an existing repo with the same passphrase.

### Known issues
- Backing up a group mailbox item may fail if it has a very large number of attachments (500+).
- Event description for exchange exports might look slightly different for certain events.
- Exchange in-place restore may restore items in well-known folders to different folders if the user has well-known folder names change based on locale and has updated the locale since the backup was created.
- In-place Exchange contacts restore will merge items in folders named "Contacts" or "contacts" into the default folder.
- External users with access through shared links will not receive these links as they are not sent via email during restore.
- Graph API has limited support for certain column types such as `location`, `hyperlink/picture`, and `metadata`. Restoring SharePoint list items containing these columns will result in differences compared to the original items.
- SharePoint list item attachments are not available due to graph API limitations.
- Group mailbox restore is not supported due to limited Graph API support for creating mailbox items.
- Due to Graph API limitations, any group mailbox items present in subfolders other than Inbox aren't backed up.

## [v0.18.0] (beta) - 2024-01-02

### Fixed
- Handle the case where an email cannot be retrieved from Exchange due to an `ErrorInvalidRecipients` error. In
this case, Corso will skip over the item but report this in the backup summary.
- Fix `ErrorItemNotFound` errors when restoring emails with multiple attachments.
- Avoid Graph SDK `Requests must contain extension changes exclusively.` errors by removing server-populated field from restored event items.
- Improve Group mailbox(conversations) backup performance by only downloading new items or items with modified content.
- Handle cases where Exchange backup stored invalid JSON blobs if there were special characters in the user content. These would result in errors during restore.

### Known issues
- Restoring OneDrive, SharePoint, or Teams & Groups items shared with external users while the tenant or site is configured to not allow sharing with external users will not restore permissions.

### Added
- Contacts can now be exported from Exchange backups as .vcf files

## [v0.17.0] (beta) - 2023-12-11

### Changed
- Memory optimizations for large scale OneDrive and Sharepoint backups.

### Fixed
- Resolved a possible deadlock when backing up Teams Channel Messages.
- Fixed an attachment download failure(ErrorTooManyObjectsOpened) during exchange backup.

## [v0.16.0] (beta) - 2023-11-28

### Added
- Export support for emails in exchange backups as `.eml` files.
- More colorful and informational cli display.

### Changed
- The file extension in Teams messages exports has switched to json to match the content type.
- SDK consumption of the /services/m365 package has shifted from independent functions to a client-based api.
- SDK consumers can now configure the /services/m365 graph api client configuration when constructing a new m365 client.
- Dynamic api rate limiting allows small-scale Exchange backups to complete more quickly.
- Kopia's local config files now uses unique filenames that match Corso configurations.  This can protect concurrent Corso operations from mistakenly clobbering storage configs during runtime.

### Fixed
- Handle OneDrive folders being deleted and recreated midway through a backup.
- Automatically re-run a full delta query on incremental if the prior backup is found to have malformed prior-state information.
- Retry drive item permission downloads during long-running backups after the jwt token expires and refreshes.
- Retry item downloads during connection timeouts.

## [v0.15.0] (beta) - 2023-10-31

### Added
- Added `corso repo update-passphrase` command to update the passphrase of an existing Corso repository
- Added Subject and Message preview to channel messages detail entries

### Fixed
- SharePoint backup would fail if any site had an empty display name
- Fix a bug with exports hanging post completion
- Handle 503 errors in nested OneDrive packages

### Changed
- Item Details formatting in Groups and Teams backups

## [v0.14.2] (beta) - 2023-10-17

### Added
- Skips graph calls for expired item download URLs.
- Export operation now shows the stats at the end of the run

### Fixed
- Catch and report cases where a protected resource is locked out of access.  SDK consumers have a new errs sentinel that allows them to check for this case.
- Fix a case where missing item LastModifiedTimes could cause incremental backups to fail.
- Email size metadata was incorrectly set to the size of the last attachment.  Emails will now correctly report the size of the mail content plus the size of all attachments.
- Improves the filtering capabilities for Groups restore and backup
- Improve check to skip OneNote files that cannot be downloaded.
- Fix Groups backup for non Team groups

### Changed
- Groups restore now expects the site whose backup we should restore

## [v0.14.0] (beta) - 2023-10-09

### Added
- Enables local or network-attached storage for Corso repositories.
- Reduce backup runtime for OneDrive and SharePoint incremental backups that have no file changes.
- Increase Exchange backup performance by lazily fetching data only for items whose content changed.
- Added `--backups` flag to delete multiple backups in `corso backup delete` command.
- Backup now includes all sites that belongs to a team, not just the root site.

### Fixed
- Teams Channels that cannot support delta tokens (those without messages) fall back to non-delta enumeration and no longer fail a backup.

### Known issues
- Restoring the data into a different Group from the one it was backed up from is not currently supported

### Other
- Groups and Teams service support is still in feature preview

## [v0.13.0] (beta) - 2023-09-18

### Added
- Groups and Teams service support available as a feature preview!  Channel messages and Files are now available for backup and restore in the CLI: `corso backup create groups --group '*'`
  - The cli commands for "groups" and "teams" can be used interchangeably, and will operate on the same backup data.
  - New permissions are required to backup Channel messages.  See the [Corso Documentation](https://corsobackup.io/docs/setup/m365-access/#configure-required-permissions) for complete details.
  Even though Channel message restoration is not available, message write permissions are included to cover future integration.
  - This is a feature preview, and may be subject to breaking changes based on feedback and testing.

### Changed
- Switched to Go 1.21
- SharePoint exported libraries are now exported with a `Libraries` prefix.

### Fixed
- Contacts backups no longer slices root-folder data if outlook is set to languages other than english.
- Failed backups if the --disable-incrementals flag was passed when there was a valid merge base under some conditions.

## [v0.12.0] (beta) - 2023-08-29

### Added
- Added `export` command to export data from OneDrive and SharePoint backups as individual files or as a single zip file.
- Restore commands now accept an optional resource override with the `--to-resource` flag.  This allows restores to recreate backup data within different mailboxes, sites, and users.  
- Improve `--mask-sensitive-data` logging mode.
- Reliability: Handle connection cancellation and resets observed when backing up or restoring large data sets.
- Reliability: Recover from Graph SDK panics when the Graph API returns incomplete responses.
- Performance: Improve backup delete performance by batching multiple storage operations into a single operation.

### Fixed
- SharePoint document libraries deleted after the last backup can now be restored.
- Restore requires the protected resource to have access to the service being restored.
- SharePoint data from multiple document libraries are not merged in exports
- `corso backup delete` was not removing the backup details data associated with that snapshot
- Fix OneDrive restores could fail with a concurrent map write error
- Fix backup list displaying backups that had errors
- Fix OneDrive backup could fail if item was deleted during backup
- Exchange backups would fail attempting to use delta tokens even if the user was over quota


## [v0.11.1] (beta) - 2023-07-20

### Fixed
- Allow repo connect to succeed when a `corso.toml` file was not provided but configuration is specified using environment variables and flags.

## [v0.11.0] (beta) - 2023-07-18

### Added
- Drive items backup and restore link shares
- Restore commands now accept an optional top-level restore destination with the `--destination` flag.  Setting the destination to '/' will restore items back into their original location.  
- Restore commands can specify item collision behavior.  Options are Skip (default), Replace, and Copy.
- Introduced repository maintenance commands to help optimize the repository as well as unreferenced data.

### Fixed
- Return a ServiceNotEnabled error when a tenant has no active SharePoint license.
- Added retries for http/2 stream connection failures when downloading large item content.
- SharePoint document libraries that were deleted after the last backup can now be restored.

### Known issues
- If a link share is created for an item with inheritance disabled
  (via the Graph API), the link shares restored in that item will
  not be inheritable by children
- Link shares with password protection can't be restored

## [v0.10.0] (beta) - 2023-06-26

### Added
- Exceptions and cancellations for recurring events are now backed up and restored
- Introduced a URL cache for OneDrive that helps reduce Graph API calls for long running (>1hr) backups
- Improve incremental backup behavior by leveraging information from incomplete backups
- Improve restore performance and memory use for Exchange and OneDrive

### Fixed
- Handle OLE conversion errors when trying to fetch attachments
- Fix uploading large attachments for emails and calendar
- Fixed high memory use in OneDrive backup related to logging
- Return a ServiceNotEnabled error when a tenant has no active SharePoint license.

### Changed
- Switched to Go 1.20
  
## [v0.9.0] (beta) - 2023-06-05

### Added
- Added ProtectedResourceName to the backup list json output.  ProtectedResourceName holds either a UPN or a WebURL, depending on the resource type.
- Rework base selection logic for incremental backups so it's more likely to find a valid base.
- Improve OneDrive restore performance by paralleling item restores

### Fixed
- Fix Exchange folder cache population error when parent folder isn't found.
- Fix Exchange backup issue caused by incorrect json serialization
- Fix issues with details model containing duplicate entry for api consumers

### Changed
- Do not display all the items that we restored at the end if there are more than 15. You can override this with `--verbose`.

## [v0.8.0] (beta) - 2023-05-15

### Added
- Released the --mask-sensitive-data flag, which will automatically obscure private data in logs.
- Added `--disable-delta` flag to disable delta based backups for Exchange
- Permission support for SharePoint libraries.

### Fixed
- Graph requests now automatically retry in case of a Bad Gateway or Gateway Timeout.
- POST Retries following certain status codes (500, 502, 504) will re-use the post body instead of retrying with a no-content request.
- Fix nil pointer exception when running an incremental backup on SharePoint where the base backup used an older index data format.
- --user and --mailbox flags have been removed from CLI examples for details and restore commands (they were already not supported, this only updates the docs).
- Improve restore time on large restores by optimizing how items are loaded from the remote repository.
- Remove exchange item filtering based on m365 item ID via the CLI.
- OneDrive backups no longer include a user's non-default drives.
- OneDrive and SharePoint file downloads will properly redirect from 3xx responses.
- Refined oneDrive rate limiter controls to reduce throttling errors.
- Fix handling of duplicate folders at the same hierarchy level in Exchange. Duplicate folders will be merged during restore operations.
- Fix backup for mailboxes that has used up all their storage quota
- Restored folders no longer appear in the Restore results. Only restored items will be displayed.

### Known Issues
- Restore operations will merge duplicate Exchange folders at the same hierarchy level into a single folder.
- Sharepoint SiteGroup permissions are not restored.
- SharePoint document library data can't be restored after the library has been deleted.

## [v0.7.0] (beta) - 2023-05-02

### Added
- Permissions backup for OneDrive is now out of experimental (By default, only newly backed up items will have their permissions backed up. You will have to run a full backup to ensure all items have their permissions backed up.)
- LocationRef is now populated for all services and data types. It should be used in place of RepoRef if a location for an item is required.
- User selection for Exchange and OneDrive can accept either a user PrincipalName or the user's canonical ID.
- Add path information to items that were skipped during backup because they were flagged as malware.

### Fixed
- Fixed permissions restore in latest backup version.
- Incremental OneDrive backups could panic if the delta token expired and a folder was seen and deleted in the course of item enumeration for the backup.
- Incorrectly moving subfolder hierarchy from a deleted folder to a new folder at the same path during OneDrive incremental backup.
- Handle calendar events with no body.
- Items not being deleted if they were created and deleted during item enumeration of a OneDrive backup.
- Enable compression for all data uploaded by kopia.
- SharePoint --folder selectors correctly return items.
- Fix Exchange cli args for filtering items
- Skip OneNote items bigger than 2GB (Graph API prevents us from downloading them)
- ParentPath of json output for Exchange calendar now shows names instead of IDs.
- Fixed failure when downloading huge amount of attachments
- Graph API requests that return an ECONNRESET error are now retried.
- Fixed edge case in incremental backups where moving a subfolder, deleting and recreating the subfolder's original parent folder, and moving the subfolder back to where it started would skip backing up unchanged items in the subfolder.
- SharePoint now correctly displays site urls on `backup list`, instead of the site id.
- Drives with a directory containing a folder named 'folder' will now restore without error.
- The CORSO_LOG_FILE env is appropriately utilized if no --log-file flag is provided.
- Fixed Exchange events progress output to show calendar names instead of IDs.
- Fixed reporting no items match if restoring or listing details on an older Exchange backup and filtering by folder.
- Fix backup for mailboxes that has used up all their storage quota

### Known Issues
- Restoring a OneDrive or SharePoint file with the same name as a file with that name as its M365 ID may restore both items.
- Exchange event restores will display calendar IDs instead of names in the progress output.

## [v0.6.1] (beta) - 2023-03-21

### Added
- Sharepoint library (document files) support: backup, list, details, and restore.
- OneDrive item downloads that return 404 during backup (normally due to external deletion while Corso processes) are now skipped instead of quietly dropped.  These items will appear in the skipped list alongside other skipped cases such as malware detection.
- Listing a single backup by id will also list the skipped and failed items that occurred during the backup.  These can be filtered out with the flags `--failed-items hide`, `--skipped-items hide`, and `--recovered-errors hide`.
- Enable incremental backups for OneDrive if permissions aren't being backed up.
- Show progressbar while files for user are enumerated
- Hidden flag to control parallelism for fetching Exchange items (`--fetch-parallelism`). May help reduce `ApplicationThrottled` errors but will slow down backup.

### Fixed
- Fix repo connect not working without a config file
- Fix item re-download on expired links silently being skipped
- Improved permissions backup and restore for OneDrive

### Known Issues
- Owner (Full control) or empty (Restricted View) roles cannot be restored for OneDrive
- OneDrive will not do an incremental backup if permissions are being backed up.
- SharePoint --folder selection in details and restore always return "no items match the specified selectors".
- Event instance exceptions (ie: changes to a single event within a recurring series) are not backed up.

## [v0.5.0] (beta) - 2023-03-13

### Added
- Show owner information when doing backup list in json format
- Permissions for groups can now be backed up and restored
- Onedrive files that are flagged as malware get skipped during backup.  Skipped files are listed in the backup results as part of the status, including a reference to their categorization, eg: "Completed (0 errors, 1 skipped: 1 malware)".

### Fixed
- Corso-generated .meta files and permissions no longer appear in the backup details.
- Panic and recovery if a user didn't exist in the tenant.

### Known Issues
- Folders and Calendars containing zero items or subfolders are not included in the backup.
- OneDrive files ending in `.meta` or `.dirmeta` are omitted from details and restores.
- Backups generated prior to this version will show `0 errors` when listed, even if error count was originally non-zero.

## [v0.4.0] (beta) - 2023-02-20

### Fixed
- Support for item.Attachment:Mail restore
- Errors from duplicate names in Exchange Calendars
- Resolved an issue where progress bar displays could fail to exit, causing unbounded CPU consumption.
- Fix Corso panic within Docker images
- Debugging with the CORSO_URL_LOGGING env variable no longer causes accidental request failures.
- Don't discover all users when backing up each user in a multi-user backup

### Changed
- When using Restore and Details on Exchange Calendars, the `--event-calendar` flag can now identify calendars by either a Display Name or a Microsoft 365 ID.
- Exchange Calendars storage entries now construct their paths using container IDs instead of display names.  This fixes cases where duplicate display names caused system failures.

### Known Issues
- Nested attachments are currently not restored due to an [issue](https://github.com/microsoft/kiota-serialization-json-go/issues/61) discovered in the Graph APIs
- Breaking changes to Exchange Calendar backups.
- The debugging env variable CORSO_URL_LOGGING causes exchange get requests to fail.
- Onedrive files that are flagged as Malware consistently fail during backup.

## [v0.3.0] (alpha) - 2023-02-07

### Added

- Document Corso's fault-tolerance and restartability features
- Add retries on timeouts and status code 500 for Exchange
- Increase page size preference for delta requests for Exchange to reduce number of roundtrips
- OneDrive file/folder permissions can now be backed up and restored
- Add `--restore-permissions` flag to toggle restoration of OneDrive permissions
- Add versions to backups so that we can understand/handle older backup formats

### Fixed

- Added additional backoff-retry to all OneDrive queries.
- Users with `null` userType values are no longer excluded from user queries.
- Fix bug when backing up a calendar that has the same name as the default calendar

### Known Issues

- When the same user has permissions to a file and the containing
  folder, we only restore folder level permissions for the user and no
  separate file only permission is restored.
- Link shares are not restored

## [v0.2.0] (alpha) - 2023-01-29

### Fixed

- Check if the user specified for an exchange backup operation has a mailbox.

### Changed
- Item.Attachments are disabled from being restored for the patching of ([#2353](https://github.com/alcionai/corso/issues/2353))
- BetaClient introduced. Enables Corso to be able to interact with SharePoint Page objects. Package located `/internal/connector/graph/betasdk`
- Handle case where user's drive has not been initialized
- Inline attachments (e.g. copy/paste ) are discovered and backed up correctly ([#2163](https://github.com/alcionai/corso/issues/2163))
- Guest and External users (for cloud accounts) and non-on-premise users (for systems that use on-prem AD syncs) are now excluded from backup and restore operations.
- Remove the M365 license guid check in OneDrive backup which wasn't reliable.
- Reduced extra socket consumption while downloading multiple drive files.
- Extended timeout boundaries for exchange attachment downloads, reducing risk of cancellation on large files.
- Identify all drives associated with a user or SharePoint site instead of just the results on the first page returned by Graph API.

## [v0.1.0] (alpha) - 2023-01-13

### Added

- Folder entries in backup details now indicate whether an item in the hierarchy was updated
- Incremental backup support for exchange is now enabled by default.

### Changed

- The selectors Reduce() process will only include details that match the DiscreteOwner, if one is specified.
- New selector constructors will automatically set the DiscreteOwner if given a single-item slice.
- Write logs to disk by default ([#2082](https://github.com/alcionai/corso/pull/2082))

### Fixed

- Issue where repository connect progress bar was clobbering backup/restore operation output.
- Issue where a `backup create exchange` produced one backup record per data type.
- Specifying multiple users in a onedrive backup (ex: `--user a,b,c`) now properly delimits the input along the commas.
- Updated the list of M365 SKUs used to check if a user has a OneDrive license.

### Known Issues

- `backup list` will not display a resource owner for backups created prior to this release.

## [v0.0.4] (alpha) - 2022-12-23

### Added

- Incremental backup support for Exchange ([#1777](https://github.com/alcionai/corso/issues/1777)). This is currently enabled by specifying the `--enable-incrementals`  
  with the `backup create` command. This functionality will be enabled by default in an upcoming release.
- Folder entries in backup details now include size and modified time for the hierarchy ([#1896](https://github.com/alcionai/corso/issues/1896))

### Changed

- **Breaking Change**:
  Changed how backup details are stored in the repository to
  improve memory usage ([#1735](https://github.com/alcionai/corso/issues/1735))
- Improve OneDrive backup speed ([#1842](https://github.com/alcionai/corso/issues/1842))
- Upgrade MS Graph SDK libraries ([#1856](https://github.com/alcionai/corso/issues/1856))
- Docs: Add Algolia docsearch to Corso docs ([#1844](https://github.com/alcionai/corso/pull/1844))
- Add an `updated` flag to backup details ([#1813](https://github.com/alcionai/corso/pull/1813))
- Docs: Speed up Windows Powershell download ([#1798](https://github.com/alcionai/corso/pull/1798))
- Switch to Go 1.19 ([#1632](https://github.com/alcionai/corso/pull/1632))

### Fixed

- Fixed retry logic in the Graph SDK that would result in an `400 Empty Payload` error when the request was retried ([1778](https://github.com/alcionai/corso/issues/1778))([msgraph-sdk-go #341](https://github.com/microsoftgraph/msgraph-sdk-go/issues/341))
- Don't error out if a folder was deleted during an exchange backup operation ([#1849](https://github.com/alcionai/corso/pull/1849))
- Docs: Fix CLI auto-generated docs headers ([#1845](https://github.com/alcionai/corso/pull/1845))

## [v0.0.3] (alpha) - 2022-12-05

### Added

- Display backup size in backup list command (#1648) from [meain](https://github.com/meain)
- Improve OneDrive backup performance (#1607) from [meain](https://github.com/meain)
- Improve Exchange backup performance (#1608) from [meain](https://github.com/meain)
- Add flag to retain all progress bars (#1582) from [ryanfkeepers](https://github.com/ryanfkeepers)
- Fix resource owner display on backup list (#1580) from [ryanfkeepers](https://github.com/ryanfkeepers)

### Changed

- Improve logging (#1642) from [ryanfkeepers](https://github.com/ryanfkeepers)
- Generate separate backup for each resource owner (#1609) from [ashmrtn](https://github.com/ashmrtn)
- Print version info to stdout instead of stderr (#1503) from [meain](https://github.com/meain)

## [v0.0.2] (alpha) - 2022-11-14

### Added

- Added AWS X-Ray support for better observability (#1111) from [ryanfkeepers](https://github.com/ryanfkeepers)
- Allow disabling TLS and TLS verification (#1415) from [vkamra](https://github.com/vkamra)
- Add filtering based on path prefix/contains (#1224) from [ryanfkeepers](https://github.com/ryanfkeepers)
- Add info about doc owner for OneDrive files (#1366) from [meain](https://github.com/meain)
- Add end time for Exchange events from (#1366) [meain](https://github.com/meain)

### Changed

- Export `RepoAlreadyExists` error for sdk users (#1136)from [ryanfkeepers](https://github.com/ryanfkeepers)
- RudderStack logger now respects corso logger settings (#1324) from [ryanfkeepers](https://github.com/ryanfkeepers)

## [v0.0.1] (alpha) - 2022-10-24

### New features

- Supported M365 Services

  - Exchange - email, events, contacts ([RM-8](https://github.com/alcionai/corso-roadmap/issues/28))
  - OneDrive - files ([RM-12](https://github.com/alcionai/corso-roadmap/issues/28))

- Backup workflows

  - Create a full backup ([RM-19](https://github.com/alcionai/corso-roadmap/issues/19))
  - Create a backup for a specific service and all or some data types ([RM-19](https://github.com/alcionai/corso-roadmap/issues/19))
  - Create a backup for all or a specific user ([RM-20](https://github.com/alcionai/corso-roadmap/issues/20))
  - Delete a backup manually ([RM-24](https://github.com/alcionai/corso-roadmap/issues/24))

- Restore workflows

  - List, filter, and view backup content details ([RM-23](https://github.com/alcionai/corso-roadmap/issues/23))
  - Restore one or more items or folders from backup ([RM-28](https://github.com/alcionai/corso-roadmap/issues/28), [RM-29](https://github.com/alcionai/corso-roadmap/issues/29))
  - Non-destructive restore to a new folder/calendar in the same account ([RM-30](https://github.com/alcionai/corso-roadmap/issues/30))

- Backup storage

  - Zero knowledge encrypted backups with user conrolled passphrase ([RM-6](https://github.com/alcionai/corso-roadmap/issues/6))
  - Initialize and connect to an S3-compliant backup repository ([RM-5](https://github.com/alcionai/corso-roadmap/issues/5))

- Miscellaneous
  - Optional usage statistics reporting ([RM-35](https://github.com/alcionai/corso-roadmap/issues/35))

[Unreleased]: https://github.com/alcionai/corso/compare/v0.19.0...HEAD
[v0.19.0]: https://github.com/alcionai/corso/compare/v0.18.0...v0.19.0
[v0.18.0]: https://github.com/alcionai/corso/compare/v0.17.0...v0.18.0
[v0.17.0]: https://github.com/alcionai/corso/compare/v0.16.0...v0.17.0
[v0.16.0]: https://github.com/alcionai/corso/compare/v0.15.0...v0.16.0
[v0.15.0]: https://github.com/alcionai/corso/compare/v0.14.0...v0.15.0
[v0.14.0]: https://github.com/alcionai/corso/compare/v0.13.0...v0.14.0
[v0.13.0]: https://github.com/alcionai/corso/compare/v0.12.0...v0.13.0
[v0.12.0]: https://github.com/alcionai/corso/compare/v0.11.1...v0.12.0
[v0.11.1]: https://github.com/alcionai/corso/compare/v0.11.0...v0.11.1
[v0.11.0]: https://github.com/alcionai/corso/compare/v0.10.0...v0.11.0
[v0.10.0]: https://github.com/alcionai/corso/compare/v0.9.0...v0.10.0
[v0.9.0]: https://github.com/alcionai/corso/compare/v0.8.1...v0.9.0
[v0.8.0]: https://github.com/alcionai/corso/compare/v0.7.1...v0.8.0
[v0.7.0]: https://github.com/alcionai/corso/compare/v0.6.1...v0.7.0
[v0.6.1]: https://github.com/alcionai/corso/compare/v0.5.0...v0.6.1
[v0.5.0]: https://github.com/alcionai/corso/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/alcionai/corso/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/alcionai/corso/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/alcionai/corso/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/alcionai/corso/compare/v0.0.4...v0.1.0
[v0.0.4]: https://github.com/alcionai/corso/compare/v0.0.3...v0.0.4
[v0.0.3]: https://github.com/alcionai/corso/compare/v0.0.2...v0.0.3
[v0.0.2]: https://github.com/alcionai/corso/compare/v0.0.1...v0.0.2
[v0.0.1]: https://github.com/alcionai/corso/tag/v0.0.1
