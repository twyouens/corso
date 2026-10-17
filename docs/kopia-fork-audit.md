# Kopia fork audit

Chunk 0.1. Produced for PRD STO-5 ("the storage engine moves from Alcion's 2024 Kopia fork to
current upstream Kopia, unless the fork review finds a change we cannot do without") and REPO-4
("the fork is upgraded to current upstream Kopia ... before k3ep depends on it"). This document
is read-only research: no code changes. The actual upgrade is chunk 0.4's job.

## 1. The exact dependency in use today

`src/go.mod`:

```
require (
    ...
    github.com/kopia/kopia v0.15.0
    ...
)

replace (
    github.com/kopia/kopia => github.com/alcionai/kopia v0.12.2-0.20240322180947-41471159a0a4
    ...
)
```

- Module path: `github.com/kopia/kopia` (unchanged from upstream; the fork's own `go.mod` still
  declares `module github.com/kopia/kopia`, it is not renamed).
- The `require` line pins `v0.15.0`, but the `replace` directive always wins at build time, so
  the version string `v0.15.0` in the `require` line is never actually resolved or built. It is
  effectively dead text. Every build uses the `replace` target.
- The `replace` target is a Go pseudo-version: `v0.12.2-0.20240322180947-41471159a0a4`. Decoded:
  commit `41471159a0a48cf2c0f56ad100dca16a6938123c` on `github.com/alcionai/kopia`, committed
  `2024-03-22T18:09:47Z` (confirmed via the module's `.info` metadata file in the Go module
  cache, and independently via `git log` on a clone of the fork).
- That commit, `41471159`, is a merge commit: "Merge pull request #318 from
  alcionai/wrap-manifest-compaction-error".

**The pseudo-version's displayed base tag (`v0.12.2`) is misleading.** Using
`git merge-base 41471159a0a48cf2c0f56ad100dca16a6938123c <upstream kopia/kopia master>`, the
actual most-recent-common-ancestor with current upstream history is
`c56d3303834a205274e5253e1a5ffa2511d49f11` ("feat(cli): handle SIGTERM (#3562)"), dated
**2024-01-11**. That commit is 69 commits after upstream's `v0.15.0` tag (2023-10-18) and about
10 weeks before upstream tagged `v0.16.0` (2024-03-22). `git merge-base --is-ancestor` confirms
`v0.12.1`, `v0.13.0` and `v0.15.0`'s real commits are all ancestors of the fork commit, so the
fork's content is clearly newer than `v0.12.x`. The `v0.12.2` pseudo-version base most likely
reflects which release tags existed in the `alcionai/kopia` mirror at the moment Corso's
maintainers ran `go get` in March 2024 (the mirror now carries `v0.13.0`–`v0.15.0` tags too, but
Go computed and froze the pseudo-version string at pin time; it does not get recomputed later).
This is cosmetic only — Go always resolves and builds the exact commit hash, not the displayed
label — but it is worth recording so nobody mistakes "v0.12.2" for the real base.

**Base commit for this audit: `c56d3303834a205274e5253e1a5ffa2511d49f11`, 2024-01-11**, used as
the pre-fork reference point for the diff in section 2.

For context: the `alcionai/kopia` fork's `master` branch kept merging upstream commits for about
five more months after Corso pinned `41471159` (tip is `5dbc8a478a7e1ad066f5212672280751f357df52`,
2024-08-27) and has not moved since. Corso itself was archived shortly after (see
`corso-analysis.md` and the repository's own "archival notice" commits). As of this audit (run
against upstream `kopia/kopia` HEAD `01938f966dc379a74de8b5ba9fc96c2048f64232`, 2026-10-15;
`git describe --tags HEAD` gives `v0.23.1-168-g01938f96`, i.e. HEAD is 168 commits past the
latest tagged release, `v0.23.1`), the fork's base is **1,154 commits and roughly 2.75 years** behind
current upstream.

Unrelated `replace` directive, out of scope for this audit per the brief: `src/go.mod` also
redirects `github.com/xhit/go-simple-mail/v2` to an Alcion fork that "removes the validation of
email addresses as we might get incomplete email addresses." Flagging only because it exists in
the same `replace()` block; it is not a Kopia concern.

## 2. Diff summary: Alcion's fork vs. its upstream base

Diffed `c56d3303` (base, section 1) against `41471159` (pinned commit) in a clone of
`github.com/alcionai/kopia` with `kopia/kopia` added as a second remote:

```
go.mod                                      |  2 +
go.sum                                      | 10 ++++
repo/blob/s3/s3_assume_role.go              | 78 +++++++++++++++++++++++++++++
repo/blob/s3/s3_options.go                  | 13 +++--
repo/blob/s3/s3_storage.go                  | 18 +++++++
repo/blob/s3/s3_storage_test.go             | 28 +++++++++--
repo/manifest/committed_manifest_manager.go |  4 +-
repo/manifest/manifest_manager.go           | 17 +++++++
repo/manifest/manifest_manager_test.go      | 69 +++++++++++++------------
9 files changed, 197 insertions(+), 42 deletions(-)
```

Eleven commits, three logical changes, all under upstream PR #318:

**a. S3 AssumeRole credential support** (`repo/blob/s3/s3_assume_role.go` (new file),
`s3_options.go`, `s3_storage.go`). Adds `Options.RoleARN`, `SessionName`, `RoleDuration`,
`Tags` (map of STS session tags). When `RoleARN` is set, `newStorage` builds a
`stscreds.AssumeRoleProvider` (via `aws/aws-sdk-go`, pulling in `aws-sdk-go` and
`jmespath/go-jmespath` as new indirect dependencies) instead of the default static/env/IAM
credential chain. Looks like it was built so Alcion's own backend could assume a
customer-specific IAM role to reach a customer's S3 bucket cross-account, rather than needing
static per-tenant access keys — a SaaS multi-tenant access pattern.

**b. S3 custom query parameters and TLS handshake timeout** (same files). Adds
`Options.QueryParams` (added to every GET request via `minio.GetObjectOptions.AddReqParam`) and
`Options.TLSHandshakeTimeout` (seconds, applied to the HTTP transport). The query-params comment
in Corso's own `pkg/storage/storage.go` explains the "why": `x-`-prefixed params are ignored
functionally by S3 but show up in the bucket's server access logs, so this is a
request-tagging/observability hook, not a correctness fix. The TLS handshake timeout looks like
an operational hardening change (bound a slow/hanging TLS handshake) that upstream's
`http.DefaultTransport.Clone()` doesn't set.

**c. Manifest auto-compaction: configurable threshold + error wrap**
(`repo/manifest/manifest_manager.go`, `committed_manifest_manager.go`). Replaces the fixed
`autoCompactionContentCountDefault` (16) threshold with `getAutoCompactionContentCount()`, which
reads it from the `KOPIA_MANIFEST_AUTOCOMPACT_COUNT` environment variable at call time, falling
back to 16. Also changes `errors.Errorf("error auto-compacting contents")` (which discarded the
underlying error) to `errors.Wrap(err, "error auto-compacting contents")` (which keeps it). Looks
like an operational tuning knob plus a plain bug fix for swallowed errors during compaction
failures, probably added while debugging a real compaction incident.

## 3. Fork-only call sites in this repository

Searched every file under `src/` that imports `github.com/kopia/kopia/...`:
`grep -rl "kopia/kopia" --include="*.go" src/` returns **40 files**, not just
`src/internal/kopia/`. Checked each one against the fields/behaviour added in section 2 versus
stock upstream Kopia API (`repo.*`, `blob.*`, `manifest.ID`, `compression.*`, `maintenance.*`,
`format.*`, `repo/logging`, `fs`, `snapshot`, etc., all unchanged by the fork and still present
in upstream `kopia/kopia` today). Cross-checked the result by grepping the whole of `src/`
(not just these 40 files) for each fork-only field name
(`RoleARN|AssumeRole|QueryParams|TLSHandshakeTimeout|KOPIA_MANIFEST_AUTOCOMPACT_COUNT|SessionName|RoleDuration`)
independently of the import grep; that search returns hits in exactly the same two files named
below, nowhere else.

Only two files actually depend on fork-only behaviour, both via the S3 storage path:

- **`src/internal/kopia/s3.go`, function `s3BlobStorage` (lines 33–49).** Builds a
  `s3.Options{...}` literal and sets:
  - `Tags: s.SessionTags` (line 39) — field does not exist in upstream `s3.Options`.
  - `QueryParams: s.QueryParams` (line 40) — field does not exist in upstream `s3.Options`;
    no upstream mechanism adds arbitrary query params to GET requests.
  - `SessionName: s.SessionName`, `RoleARN: s.Role`, `RoleDuration: s.SessionDuration`
    (lines 41–43) — upstream now has an AssumeRole mechanism too (section 4), but with a
    different field set and a different credential-provider implementation.
  - `TLSHandshakeTimeout: 60` (line 47) — field does not exist in upstream `s3.Options`, and
    this line is **unconditional**: every S3-backed repository connection sets it, whether or
    not AssumeRole/tags/query-params are used. This is the one fork-only dependency that is not
    optional — it runs on every S3 repo open today.

- **`src/pkg/storage/storage.go`.** Public `Storage` struct carries `SessionTags`, `Role`,
  `SessionName`, `SessionDuration`, `QueryParams` (lines 47–55, commented `// TODO: These are
  AWS S3 specific -> move these out`), and `NewStorageUsingRole` (lines 68–90) is the only
  constructor that populates `Role`/`SessionName`/`SessionTags`/`QueryParams`. These exist only
  to feed `s3.go`'s fork-only fields through from the public API.

The other 39 of the 40 files checked but **not** fork-dependent, grouped by what they actually
import from `kopia/kopia` and why none of it touches section 2's three changes:

- **`internal/kopia/retention/opts.go` + `opts_test.go`** (S3 Object Lock / retention — STO-3's
  immutability requirement) use `format.BlobStorageConfiguration`, `blob.Governance`,
  `blob.Compliance`, `maintenance.Params`, `maintenance.CheckExtendRetention`. All present,
  unchanged, in current upstream (`repo/maintenance/blob_retain.go`,
  `repo/format/blobcfg_blob.go`). Retention/object lock is a stock Kopia feature, not an Alcion
  addition.
- **`internal/kopia/filesystem.go`** uses only `filesystem.Options{Path}` — stock upstream.
- **The rest of `internal/kopia/`'s non-test, non-mock files** — `conn.go`, `upload.go`,
  `wrapper.go`, `base_finder.go`, `cleanup_backups.go`, `model_store.go`, `backup_bases.go`,
  `data_collection.go` — use only core, unchanged upstream types: `repo.Repository`,
  `repo.Initialize`/`Connect`/`Open`/`WriteSession`/`DirectWriteSession`, `repo.DirectRepository`,
  `compression.Name`/`compression.ByName`, `content.CachingOptions`, `blob.Storage`,
  `manifest.ID`, `snapshot`/`snapshot/policy`/`snapshot/snapshotfs`, `fs`. None of these types or
  functions changed shape between the fork's base and upstream HEAD in a way that introduces a
  fork-only dependency (API *version* drift chunk 0.4 will hit during the upgrade is a separate
  concern from fork-only *behaviour*, which is what this section inventories).
- **`internal/kopia/`'s eleven `*_test.go` files** (`backup_bases_test.go`, `base_finder_test.go`,
  `cleanup_backups_test.go`, `conn_test.go`, `data_collection_test.go`,
  `merge_collection_test.go`, `model_store_test.go`, `upload_test.go`, `wrapper_scale_test.go`,
  `wrapper_test.go`, plus `retention/opts_test.go` already counted above) exercise the same
  non-fork types as their corresponding non-test files; none construct an `s3.Options` or touch
  the manifest auto-compaction env var.
- **`internal/kopia/mock_backup_base.go`** and **`internal/kopia/mockkopia/fs.go`** — mocks used
  by the tests above. `mock_backup_base.go` imports `repo/manifest` (for `manifest.ID`) and
  `snapshot`; `mockkopia/fs.go` imports `fs` (Kopia's virtual filesystem interface, for building
  fake snapshot trees in tests). Both stock upstream, no fork fields.
- **Inject/control-plane plumbing**: `internal/operations/inject/inject.go` and
  `pkg/control/repository/repo.go` (the latter only has a `kopia/kopia` *comment*, a link to a
  Kopia source file for context — no actual import) — both only reference `manifest.ID`-shaped
  values passed through interfaces; neither builds `s3.Options` or reads the auto-compaction env
  var.
- **Catalogue/store plumbing**: `internal/model/model.go`, `pkg/repository/backups.go`,
  `pkg/repository/debug.go`, `pkg/repository/repository_unexported_test.go`,
  `pkg/store/backup.go`, `pkg/store/backup_test.go`, `pkg/store/mock/model_store.go`,
  `pkg/store/mock/wrapper.go` — all use `manifest.ID` as an opaque identifier type (backup-point
  IDs), nothing else.
- **M365 service layer**: `internal/m365/service/groups/backup.go` and its `backup_test.go` —
  import `repo/manifest` for the same `manifest.ID` reason as the catalogue plumbing above; no
  storage-layer code.
- **Operations layer**: `internal/operations/backup_test.go` and `internal/operations/manifests_test.go`
  — import `repo/manifest` and `snapshot` for test fixtures, same stock types.
- **Logging**: `pkg/logger/logger.go` imports `repo/logging` to adapt Kopia's logger interface
  to Corso's; unchanged upstream package.
- **`cmd/s3checker/pkg/s3/s3.go`** talks to S3 directly through `minio-go` (not through
  `kopia/kopia`'s `repo/blob/s3` package at all — the `kopia/kopia` string match in this file is
  only a source-attribution comment pointing at a Kopia source file it was "scavenged" from). Not
  a dependency on the fork.

**`NewStorageUsingRole` is defined but never called anywhere in this repository** (checked `cli/`
and `cmd/` too — no flag or command wires it up). It is live, exported public API
(`pkg/storage` is one of the packages k3ep is allowed to import per REPO-2) but currently dead
code within Corso itself.

## 4. Migration options per fork-only dependency

| Fork-only field / behaviour | Upstream equivalent today | Migration |
| --- | --- | --- |
| `Options.TLSHandshakeTimeout` (set unconditionally to 60s in `s3.go`) | None. Upstream's `getCustomTransport` only handles `DoNotVerifyTLS` and `RootCA`; no handshake timeout field. | **Must carry forward or drop.** Dropping removes the 60s bound on a hanging TLS handshake during S3 connect — acceptable risk for k3ep's known, small set of S3-compatible destinations (STO-1), but it is a real behaviour change, not a no-op. Smallest patch: ~15 lines re-adding the field and the one `if` in `getCustomTransport`. |
| `Options.QueryParams` / `AddReqParam` on GET | None. | **Can be dropped.** Nothing in the PRD calls for S3 access-log request tagging; `NewStorageUsingRole`/`QueryParams` plumbing is unused dead code in this repo already (section 3). Drop the field and the `pkg/storage` plumbing that only exists to carry it. |
| `Options.Tags` (STS session tags) | No equivalent; `minio-go`'s `credentials.STSAssumeRoleOptions` (used by upstream's own AssumeRole support, see next row) has no `Tags` field at any version checked (v7.3.0, the version upstream currently vendors). | **Can be dropped** alongside `QueryParams` — same unused code path. |
| `Options.RoleARN` / `SessionName` / `RoleDuration` (AssumeRole) | **Absorbed and improved upstream.** Current upstream `s3_storage.go` (`newStorage`) has its own `RoleARN`, `SessionName`, `RoleDuration` (as `jsonencoding.Duration`), plus `RoleEndpoint`/`RoleRegion`, and additionally a full WebIdentity (OIDC) flow (`WebIdentityToken`/`WebIdentityTokenFile`) that the fork never had. It uses `minio-go`'s own `credentials.NewSTSAssumeRole`/`NewSTSWebIdentity` instead of pulling in `aws-sdk-go` + `jmespath` as extra dependencies. | **Use the upstream mechanism if k3ep ever needs AssumeRole** (not required by STO-1 today — destinations are defined by endpoint/bucket/prefix/region/credentials, no IAM role). Since `NewStorageUsingRole` is unused in this repo already, nothing needs to be carried forward; if k3ep later wants this, `internal/engine` (k3ep side) or a future fork chunk wires the upstream `RoleARN`/`RoleEndpoint` fields instead of re-adding the fork's version. |
| Manifest auto-compaction `KOPIA_MANIFEST_AUTOCOMPACT_COUNT` env var | **Superseded upstream, better.** Upstream's `manifest.ManagerOptions.AutoCompactionThreshold` is a real, typed, programmatic option passed into the manager constructor (`repo/manifest/manifest_manager.go`), not an environment-variable side channel. | **Drop.** Confirmed by grep that Corso never reads or sets this env var anywhere in `src/`. If k3ep ever needs a non-default threshold, use the upstream `ManagerOptions` field directly — cleaner than an env var. |
| `errors.Errorf(...)` → `errors.Wrap(err, ...)` in `loadCommittedContentsLocked` (swallowed-error fix) | **Already fixed upstream**, independently of this fork. Current `repo/manifest/committed_manifest_manager.go` already does `errors.Wrap(err, "error auto-compacting contents")`. | **Nothing to carry forward.** Moving to current upstream keeps this fix automatically; it was never Corso-specific, it was a genuine upstream bug that has since been fixed on its own. |

## 5. Recommendation

**Move to current upstream Kopia (`github.com/kopia/kopia`, currently `v0.23.1`); do not keep a
rebased fork.**

Reasons:

- Of the fork's three real changes, two (manifest auto-compaction configurability, the error-wrap
  fix) are already superseded by better upstream mechanisms or already fixed upstream outright.
  Nothing to carry forward for those.
- Of the remaining one (S3 AssumeRole/Tags/QueryParams/TLSHandshakeTimeout cluster), only
  `TLSHandshakeTimeout` is actually exercised unconditionally by Corso today, and it is a small,
  self-contained ~15-line patch, not a reason to carry a whole forked module. `Tags` and
  `QueryParams` back dead code (`NewStorageUsingRole` is never called). `RoleARN`/`SessionName`/
  `RoleDuration` have a better upstream replacement already (plus OIDC web-identity, which the
  fork never had) if k3ep ever needs AssumeRole — not required by STO-1 as written.
- The fork is 1,154 commits and ~2.75 years behind upstream, covering 2.75 years of upstream bug
  fixes (including, concretely, the swallowed-compaction-error bug this fork separately
  patched and upstream separately fixed), security patches and dependency bumps the fork will
  never receive (`alcionai/kopia` itself stopped moving in August 2024). Maintaining a rebased
  fork just to keep a 60-second TLS timeout is not a good trade against picking up 2.75 years of
  upstream fixes k3ep would otherwise be permanently missing.
- REPO-4 explicitly wants the fork upgraded to current upstream Kopia before k3ep depends on it;
  STO-5 only blocks that move if the fork review finds a change k3ep "cannot do without." Nothing
  found here rises to that bar — the one unconditional behaviour (TLS handshake timeout) is a
  small, independently reproducible patch, not a reason to keep tracking a stale fork.

If a rebased fork were chosen instead (not recommended), it would need to carry exactly one
patch: re-add `Options.TLSHandshakeTimeout` and the corresponding `if opt.TLSHandshakeTimeout != 0`
branch in `getCustomTransport` (`repo/blob/s3/s3_storage.go`). No other patch from section 2
needs to survive, per section 4.

## 6. Follow-on work for chunk 0.4

Ordered, with risk per item:

1. **Remove the `kopia/kopia` → `alcionai/kopia` `replace` directive and bump `require` to
   current upstream (`v0.23.1` or later at the time 0.4 runs).** *Risk: medium.* It's a one-line
   `go.mod` change but pulls in ~2.75 years of upstream API churn across `repo/`, `snapshot/`,
   `fs/`, `maintenance/` — every call site in `internal/kopia/` (33 files, section 3) needs
   recompiling and checking against the new signatures, not just the fork-only ones.
2. **Re-implement the S3 TLS handshake timeout** (`internal/kopia/s3.go`'s
   `TLSHandshakeTimeout: 60`) against upstream's `getCustomTransport`, since upstream has no such
   field (section 4). *Risk: low.* Small, isolated, upstream's function shape is simple enough
   to patch in `internal/kopia/s3.go` itself (wrap the returned `blob.Storage`/transport) without
   touching the vendored dependency, if a local wrapper is preferred over carrying a fork patch.
3. **Drop `Tags`, `QueryParams`, `NewStorageUsingRole` from `pkg/storage/storage.go` and the
   corresponding fields passed in `internal/kopia/s3.go`.** *Risk: low.* Confirmed dead code
   (section 3); deleting it is safe, but double check no k3ep-side code outside this repo has
   started depending on `NewStorageUsingRole` by the time 0.4 runs (it's public API — anything in
   `k3ep/internal/engine` that may have appeared between this chunk and 0.4 should be checked).
4. **Decide whether to carry `RoleARN`/`SessionName`/`RoleDuration` at all.** *Risk: low*, because
   it's currently unused (section 3), but if any later chunk decides k3ep needs AssumeRole-style
   cross-account S3 access, wire it against upstream's richer `RoleARN`/`RoleEndpoint`/
   `RoleRegion`/WebIdentity fields, not a re-added fork patch.
5. **Re-run `go build ./... && go test ./...`, then `golangci-lint run`, after the upgrade and
   confirm `internal/kopia/retention/opts.go` still passes its tests** (STO-3 immutability path)
   since it's the other consumer of Kopia APIs that changed meaningfully across 2.75 years
   (`maintenance.Params`, `format.BlobStorageConfiguration`). *Risk: medium* — retention/object
   lock semantics are exactly the kind of thing that could have shifted subtly upstream and the
   existing unit tests may not cover every edge upstream now handles differently.
6. **Check the Go version floor Kopia v0.23.1 requires against the Go version chunk 0.2 picks.**
   *Risk: low*, informational only — this chunk did not check it (out of scope, Go version is
   0.2's job), but 0.4 should confirm 0.2's chosen Go version satisfies upstream Kopia's current
   `go.mod` `go` directive before assuming the upgrade is a drop-in.

## Appendix: method

- `src/go.mod` / `src/go.sum` read directly; pseudo-version decoded via the `.info` file Go
  writes to its module cache after `go mod download github.com/kopia/kopia` (module commands run
  with `GO111MODULE=on GOWORK=off`; this machine's global Go env otherwise defaults to
  `GO111MODULE=off`, which silently ignores `go.mod` — worth knowing if another chunk hits the
  same thing and gets confused by `go build` behaving like GOPATH mode).
- `github.com/alcionai/kopia` and `github.com/kopia/kopia` were both cloned fresh (shallow
  history disabled) outside this repository; `merge-base`, `describe --tags`, `log`, and `diff`
  were run between them, never inside this worktree. No files from those clones were copied in.
- Fork-only call sites were found by `grep -rl "kopia/kopia" --include="*.go" src/`, then manually
  checking each file's actual symbol usage against the diff in section 2 and against the current
  upstream source for the same symbols.
