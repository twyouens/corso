# Corso fork (k3ep engine)

Go library that backs up and restores Microsoft 365 data (Exchange, OneDrive, SharePoint) into encrypted Kopia repositories. k3ep imports it as its backup engine. Forked from alcionai/corso (Apache 2.0). This repo owns everything that talks to Microsoft Graph or Kopia; it knows nothing about k3ep tenants, policies, users or the database.

## Commands

All Go commands run from `src/`.

| Task | Command |
| --- | --- |
| Build | `cd src && go build ./...` |
| Unit tests | `cd src && go test ./...` |
| Lint | `cd src && golangci-lint run` (verify config in src/.golangci.yml) |
| Everything CI runs | `cd src && make check` (added by chunk 0.2) |
| Integration tests (test tenant) | `scripts/with-test-tenant.sh env GO111MODULE=on make -C worktrees/corso-<chunk-id>/src test-integration` from the workspace root. The `env GO111MODULE=on` is load-bearing on this machine and easy to lose: the `test-integration` target sets `GOWORK=off` but not `GO111MODULE`, and a bare `make` does not inherit the shell's value into the recipe, so without it the suite fails with `[setup failed]` in GOPATH mode (chunk 0.7 hit this). Scoped to `./internal/seed/...` only - the test-tenant harness's own smoke test (chunk 0.3), not the pre-existing backup/restore integration suites elsewhere in this repo, which target a different (Alcion) test tenant and belong to chunks 0.5-0.7. |
| Seed the test tenant | `cd src && GO111MODULE=on GOWORK=off <workspace>/scripts/with-test-tenant.sh go run ./cmd/seed` (added by chunk 0.3; must run from `src/` - there is no `go.mod` at this repository's root) |
| Build-check without the workspace | `cd src && GOWORK=off go build ./... && GOWORK=off go test ./...` |

This machine's global Go configuration sets `GO111MODULE=off`; every command above needs `GO111MODULE=on` set explicitly alongside `GOWORK=off`, or module resolution silently falls back to GOPATH mode and every import fails to resolve. Several agents have lost time to this.

The test-tenant env file must define `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`, plus `SEED_USER_ID` and `SEED_SITE_ID`: an existing, already-licensed M365 user and an existing SharePoint site that chunk 0.3's harness seeds dedicated, prefixed folders into. The harness creates neither; both must already exist in the test tenant.

## Layout

- `src/pkg/` - public API. The only code k3ep may import. Treat every exported name here as a contract.
- `src/pkg/engine/` - **the package k3ep actually imports** (added by chunk 0.6): a thin façade over `pkg/repository` whose exported surface uses only plain Go types and other already-public `src/pkg` types, so a caller in a different module never needs to - and, because Go forbids importing another module's `internal` packages, never can - name an `internal` type. Open a repository, back up with caller-supplied tags and a progress callback, list/delete backup points by tag, run maintenance, and browse a backup point's folder tree, list the items in a folder and find items by name (chunk 0.7). New capabilities aimed at k3ep belong here, not as new exported methods on `pkg/repository` itself (which predates this chunk, has a much wider CLI-era surface, and still leaks `internal/operations` types from several of its own methods).
- `src/pkg/repository/` - open and initialise repositories; entry point for backup, restore and export operations. The fork's and CLI's own internal entry point; `pkg/engine` wraps it rather than replacing it.
- `src/pkg/services/m365/api/` - Microsoft Graph clients (mail, contacts, events, drives, sites, lists).
- `src/internal/operations/` - backup, restore and export orchestration.
- `src/internal/m365/` - per-workload collection and restore logic.
- `src/internal/kopia/` - Kopia wrapper: snapshots, upload, connection, S3 and filesystem storage.
- `src/cli/` - the original Corso CLI. Kept as a developer and recovery tool, not the product interface.
- `src/cmd/` - small programs: examples for the library API and the test-tenant seeder. `src/cmd/backupexample` (chunk 0.6) is the library API's own worked example, and the one place that mechanically proves it imports no `internal` package - see its `import_test.go`.
- `docs/` - fork decisions, such as the Kopia audit from chunk 0.1.
- `website/` - original Corso docs site. Do not change unless a chunk says so.

## Conventions

- New capabilities for k3ep go behind a deliberate public API in `src/pkg/`. Keep implementation in `src/internal/`; promote only the interface k3ep needs, and document every exported function with what it guarantees.
- Library functions take a `context.Context` first and never read configuration from environment variables, files or flags. The caller supplies storage settings, credentials and the repository key as arguments.
- Never log, print or return the repository key, M365 secrets or item content. Errors may name items by ID, not by subject or file name.
- Long-running operations report progress through a callback the caller passes in; they never write to stdout.
- Backup points carry caller-supplied tags (k3ep uses tenant, policy and object). The library stores and filters by tags but never interprets them.
- Graph calls go through the existing clients in `src/pkg/services/m365/api/`, which handle paging, throttling and retries. Do not call Graph from anywhere else.
- Beta Graph endpoints are allowed only where the fork already uses them (calendar delta, SharePoint Pages). Any new one needs a stable-API fallback and a note in `docs/`.
- Integration tests carry the build tag and naming the repo already uses for them, and only touch data the seeder created.
- Every behaviour change gets an entry in `CHANGELOG.md`; releases are tagged `vX.Y.Z` by a human.
- If a chunk adds a new `src/pkg/` surface meant for k3ep, prove it never requires an `internal` import with a test that parses the consuming example's own source and checks its import paths - not by inspection. Go's internal-import restriction only fires across modules; nothing stops an example living in this same module from importing `internal` anyway, so only a mechanical check in-repo catches a regression. See `src/cmd/backupexample/import_test.go`. That check only sees the example's own direct import declarations: a same-module value returned through Go's type-inference "escape hatch" and never explicitly imported, a subdirectory package, a `_test.go` helper, or a build-tag-gated file all fall outside it - see the test's own doc comment for the full list.
- That consumer-side check is only half of it, and on its own it proves nothing about the `src/pkg/` surface itself, because a package in `src/pkg/` legitimately imports `src/internal` for its *implementation* (`pkg/engine` imports `internal/model`, `internal/events` and `internal/version`). The other half, added by chunk 0.7, parses the `src/pkg/` package's own source and fails if any *exported* signature - function, method, exported struct field, interface method, type alias or exported variable - names a type from `src/internal`, including one laundered through an unexported local type alias. See `src/pkg/engine/exported_api_test.go`. Both checks are syntactic and neither runs the type checker, so a type reached only through inference is invisible to both; Go's own cross-module internal rule, exercised by compiling an out-of-module program against the package, remains the guarantee k3ep actually relies on.
- Errors name items by ID, not by name - and `pkg/engine`'s browse API is where that bites, since returning names is its whole purpose. The rule it follows, and which later chunks that return names should follow too: names belong in returned data, never in an error string or a log line. `pkg/engine/browse.go` identifies a backup point by ID and an entry by its opaque entry ID in every error it returns, and `TestBrowse_ErrorsNeverNameAnItem` enforces it.
- A repository connection's Microsoft 365 data provider is reused across every `Backup`/`Restore`/`Export` call made on it (see `pkg/repository`'s `connectToM365`), and its resource lookup handler (Users/Groups/Sites) is tied to whichever `path.ServiceType` was passed the *last* time `ConnectDataProvider` ran - including `path.UnknownService`, which `pkg/engine` passes while opening a repository before any service has been chosen. A new call site that reuses an existing `*m365.Controller` for a different service must call `Controller.EnsureResourceHandler` with that service first, or risk a nil-handler panic the fork's own tests never catch (the CLI always opens a repository already committed to one service).

## Do not

- Do not import anything k3ep-specific or add a database, HTTP server or scheduler here.
- Do not reintroduce usage telemetry; chunk 0.2 removes it.
- Do not change the module path, or add `replace` directives, without a chunk that says so.
- Do not edit generated code or `vendor/` by hand.
