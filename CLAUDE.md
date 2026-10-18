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
- `docs/` - fork decisions: the Kopia audit from chunk 0.1, the test-tenant harness from 0.3, and a
  one-page pointer to the library guide.
- `src/pkg/engine/README.md` - **the k3ep-facing library guide** (chunk 0.8): the call shape for each
  capability, the eleven operational-contract facts a caller has to know, the `go.work` and `GOWORK=off`
  instructions, and how a release is tagged and pinned. It lives inside the module, not in `docs/`, so
  `docs_test.go` can `//go:embed` it - see the convention below.
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
- Every behaviour change gets an entry in `CHANGELOG.md`; releases are tagged by a human, never by an
  agent. **The tag name is `src/vX.Y.Z`, not `vX.Y.Z`**: this module lives in the `src/` subdirectory,
  so Go only recognises a version tag carrying that prefix, and resolves it as module version
  `vX.Y.Z` (`go get github.com/twyouens/corso/src@vX.Y.Z`). A bare `vX.Y.Z` tag is not a version of
  this module at all. The first such release is `src/v0.1.0`; the fork's own numbering restarts
  there, and `CHANGELOG.md`'s `v0.19.0` and earlier sections are upstream Corso's history.
  Tag only the release commit, after CI is green on `main`, and treat the tag as irreversible: once
  `proxy.golang.org` has served a version it keeps serving it, and the checksum database has already
  recorded that version's hash, so a moved tag gives consumers a checksum mismatch rather than new
  code. The supported remedy for a bad release is a `retract` directive and a new version.
  `src/pkg/engine/README.md` section 1 has the full procedure.
- **S3 storage requires explicit credentials, everywhere, with no opt-out.** `pkg/storage.S3Config.validate()` rejects a blank or whitespace-only access key or secret key for every caller, CLI included; chunk 0.9 deleted the `AllowAmbientCredentials` opt-out chunk 0.6 had added, on the user's decision, because Kopia's S3 blob store falls back through its AWS SDK's own credential chain (environment, shared credentials file, EC2/ECS instance role) for any credential left blank, and a caller-chosen bucket and endpoint served by the platform's identity is the hole 0.6's security review proved live. Do not re-add an opt-out, under any name, flag or config-map key, without a chunk that says so: `TestS3Config_HasNoCredentialOptOutField` and the eight forged-key spellings in `pkg/storage/s3_test.go`, `pkg/repository/s3_credentials_test.go` and `internal/kopia/s3_credentials_test.go` all exist to stop it happening quietly. Credentials may still be *supplied* by flag, config file or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` (which `ApplyConfigOverrides` reads itself); what is gone is the fallback to a source this code never saw.
- **To prove a path makes no network request, measure it from the endpoint's side.** `pkg/storage/testdata.NewS3RequestRecorder(t)` is a loopback HTTP server to use as an S3 endpoint; it reports the method and path of every request received and whether any carried an `Authorization` header, and never a header value. Pair every "zero requests" assertion with a **positive control** on a second recorder, proving the same route with valid inputs does reach the endpoint - otherwise zero is equally consistent with an instrument that cannot see anything. Both S3 credential tests do this.
- **The repository config filename derives from `GetStorageConfigHash()`**, so changing which `S3Config`/`FilesystemConfig` fields are hashed strands every existing repository. `pkg/storage`'s `TestGetStorageConfigHash_PinnedValues` pins the values by exact string; if a change moves them, that is a product decision, not a refactor. Note that the hash is credential-sensitive today: `excludedS3ConfigFieldsForHashing` filters by top-level field name and the embedded `credentials.AWS` is one field named `AWS`, so its `AccessKey`/`SecretKey`/`SessionToken` entries match nothing.
- If a chunk adds a new `src/pkg/` surface meant for k3ep, prove it never requires an `internal` import with a test that parses the consuming example's own source and checks its import paths - not by inspection. Go's internal-import restriction only fires across modules; nothing stops an example living in this same module from importing `internal` anyway, so only a mechanical check in-repo catches a regression. See `src/cmd/backupexample/import_test.go`. That check only sees the example's own direct import declarations: a same-module value returned through Go's type-inference "escape hatch" and never explicitly imported, a subdirectory package, a `_test.go` helper, or a build-tag-gated file all fall outside it - see the test's own doc comment for the full list.
- That consumer-side check is only half of it, and on its own it proves nothing about the `src/pkg/` surface itself, because a package in `src/pkg/` legitimately imports `src/internal` for its *implementation* (`pkg/engine` imports `internal/model`, `internal/events` and `internal/version`). The other half, added by chunk 0.7, parses the `src/pkg/` package's own source and fails if any *exported* signature - function, method, exported struct field, interface method, type alias or exported variable - names a type from `src/internal`, including one laundered through an unexported local type alias. See `src/pkg/engine/exported_api_test.go`. Both checks are syntactic and neither runs the type checker, so a type reached only through inference is invisible to both; Go's own cross-module internal rule, exercised by compiling an out-of-module program against the package, remains the guarantee k3ep actually relies on.
- Errors name items by ID, not by name - and `pkg/engine`'s browse API is where that bites, since returning names is its whole purpose. The rule it follows, and which later chunks that return names should follow too: names belong in returned data, never in an error string or a log line. `pkg/engine/browse.go` identifies a backup point by ID and an entry by its opaque entry ID in every error it returns, and `TestBrowse_ErrorsNeverNameAnItem` enforces it for each of `browseEntries`' four entry error paths with one fixture per branch. Note what that test can and cannot do, since it is the model for later chunks: `browseEntries` returns on the first error, so one fixture reaches exactly one branch, and a table with a case per branch is the only way to cover them all - the first version of this test used a single fixture, claimed in its own comment to cover every path, and in fact left three of the four branches unreachable and a name interpolated into any of them undetected. If you add an error path that carries entry context, add its case; the test cannot tell you that you forgot.
- **A change to `pkg/engine`'s exported surface is a change to `src/pkg/engine/README.md`.** That guide is what
  a k3ep developer reads instead of rediscovering a finding that took an adversarial review to surface, so
  a new exported declaration belongs in it, and so does any new operational trap. Two mechanical guards,
  neither of which can check whether the prose is *true*:
  `src/pkg/engine/docs_test.go`'s `TestLibraryAPIDoc_MatchesExportedSurface` fails if the guide names an
  `engine.Symbol` or a `Type.Member` that does not exist, or if an exported top-level declaration is named
  nowhere in the guide; and `src/pkg/engine/example_test.go` holds one compiled godoc example per
  capability area (no `Output:` comment, so `go test` compiles them and never runs them), which is where
  the guide's snippets come from.
  **The guide lives inside the module (`src/pkg/engine/README.md`) and `docs_test.go` embeds it with
  `//go:embed`, and both of those are load-bearing.** Review round 1 of 0.8 found the first version, with
  the guide at `docs/library-api.md` outside the module, was not actually enforced: Go's test cache does
  not treat a file outside the module as a test input, so editing only the markdown returned
  `ok (cached)` - and CI was no better, because `.github/workflows/ci.yml` restores `GOCACHE` keyed only
  on `hashFiles('src/go.sum')` while `make check` runs `go test ./...` with no `-count=1`. Embedding makes
  the markdown a build input of the test binary, so an edit to it cannot be cached past, and deleting it
  is a compile error (`pattern README.md: no matching files found`). If a later chunk adds a check that
  reads a file **outside** `src/`, assume the same hole until it proves otherwise - and note that
  `-count=1` belongs in that chunk's own command, not in the `Makefile`'s `check` target, which would
  disable test caching for every chunk.
- **The Graph concurrency limiter is per client, and there is no process-global one to initialise.**
  Chunk 0.10 removed `graph.InitializeConcurrencyLimiter`, `api.InitConcurrencyLimit` and the
  package-level `concurrencyLimitMiddlewareSingleton`/`sync.Once` they latched. A client's capacity
  comes from the `control.Options.Parallelism.ItemFetch` its caller passed (`api.NewClient` turns it
  into a `graph.ConcurrencyLimit` option), is clamped to 1-4, and is installed on every client
  `graph.kiotaMiddlewares` builds - unconditionally, so no code path can produce a Graph client with
  no limiter. Do not add a package-level limiter or latch back:
  `TestGraphPackage_HasNoPackageLevelConcurrencyLimiterState` parses the `graph` package's own sources
  and fails on any package-level `var` mentioning `concurrencyLimiterMiddleware` or `sync.Once`. Test
  suites no longer need to initialise anything before their first Graph call. Two things stay
  process-wide and are not this convention: the per-minute rate limiters in
  `concurrency_middleware.go` (package-level by design, and they bound rate rather than simultaneity),
  and `graph.internalMiddleware` - the Requester's chain, used for arbitrary URLs and large item
  content - which carries no concurrency limiter at all, upstream included.
  **If a chunk needs to observe a client's capacity, read it rather than the value requested:**
  `adapterWrap.ConcurrencyLimit()` returns the capacity of the semaphore actually installed, because a
  mutation during 0.10 showed that a test reading the *requested* value passes unchanged against a
  reinstated process-global limiter. `adapterWrap.HTTPClientTimeout()` (0.10a) does the same for the
  timeout, reading it off the `http.Client` the adapter actually sends through.
- **A "pinned by" claim must name a test that fails when the property breaks *where the claim says
  it lives*.** 0.10's changelog said `api.newLargeItemService` appending `NoTimeout()` last was
  pinned by `graph`'s `TestPopulateConfig_LastOptionWins`; review moved `NoTimeout()` to the front
  and every test stayed green, because that test pins `graph`'s option-ordering semantics and cannot
  see the order `api` builds its slice in. Before writing "pinned by X" in a comment or the
  changelog, make the regression and watch X go red.
- A repository connection's Microsoft 365 data provider is reused across every `Backup`/`Restore`/`Export` call made on it (see `pkg/repository`'s `connectToM365`), and its resource lookup handler (Users/Groups/Sites) is tied to whichever `path.ServiceType` was passed the *last* time `ConnectDataProvider` ran - including `path.UnknownService`, which `pkg/engine` passes while opening a repository before any service has been chosen. A new call site that reuses an existing `*m365.Controller` for a different service must call `Controller.EnsureResourceHandler` with that service first, or risk a nil-handler panic the fork's own tests never catch (the CLI always opens a repository already committed to one service).

## Do not

- Do not import anything k3ep-specific or add a database, HTTP server or scheduler here.
- Do not reintroduce usage telemetry; chunk 0.2 removes it.
- Do not change the module path, or add `replace` directives, without a chunk that says so.
- Do not edit generated code or `vendor/` by hand.
