package seed

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/engine"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
	"github.com/twyouens/corso/src/pkg/storage"
)

// This file is chunk 0.6's live proof of pkg/engine against the real test
// tenant: it opens a real Kopia repository (filesystem and, when a local
// MinIO container is available, S3-compatible) using nothing but
// pkg/engine's own public API, backs up the seeded user's Exchange Inbox
// with caller-supplied tags, proves the progress callback fires and that
// nothing pkg/engine does writes to stdout, lists and deletes backup
// points by tag (including the required negative case), and runs
// maintenance.
//
// Lives in internal/seed, not pkg/engine, for the same reason chunk 0.5's
// GraphClientIntegrationSuite does (see api_integration_test.go's own doc
// comment): it needs this package's Config/RequiredEnvVars/MissingVars and
// is picked up by `make test-integration` automatically because that
// target is scoped to ./internal/seed/.... pkg/engine itself does not
// import this package - only this test file does, in the opposite,
// already-accepted direction.
//
// Credentials and MissingVars/RequiredEnvVars are read, never logged. No
// M365 user, group or site is created, modified or deleted: seed.Run below
// is the same idempotent find-or-create call 0.3 and 0.5 already proved
// safe against this tenant, and everything downstream is a read from
// Graph (via the backup) and writes to a local, disposable Kopia
// repository - never back to M365.
//
// Minio-backed env vars reuse chunk 0.4's own CORSO_TEST_MINIO_* names
// (internal/kopia/minio_roundtrip_test.go) for consistency; that file's
// consts are unexported so these are redeclared here with identical
// values and meaning, not re-exported.
const (
	minioEndpointEnv  = "CORSO_TEST_MINIO_ENDPOINT"
	minioBucketEnv    = "CORSO_TEST_MINIO_BUCKET"
	minioAccessKeyEnv = "CORSO_TEST_MINIO_ACCESS_KEY"
	minioSecretKeyEnv = "CORSO_TEST_MINIO_SECRET_KEY"
)

type EngineIntegrationSuite struct {
	tester.Suite
}

func TestEngineIntegrationSuite(t *testing.T) {
	suite.Run(t, &EngineIntegrationSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{RequiredEnvVars},
			tester.CorsoNightlyTests),
	})
}

// engineTestConfig re-validates every required credential's actual value
// (not just its name - see internal/tester.MustGetEnvVars' known bug,
// disclosed in 0.3 and 0.5's reports and still unfixed) before building a
// Config, the same guard 0.5's GraphClientIntegrationSuite uses.
func engineTestConfig(t *testing.T, manifestPath string) Config {
	t.Helper()

	if missing := MissingVars(os.Getenv, RequiredEnvVars); len(missing) > 0 {
		t.Fatal(MissingVarsError(missing))
	}

	return Config{
		TenantID:     os.Getenv(EnvTenantID),
		ClientID:     os.Getenv(EnvClientID),
		ClientSecret: os.Getenv(EnvClientSecret),
		UserID:       os.Getenv(EnvSeedUserID),
		SiteID:       os.Getenv(EnvSeedSiteID),
		ManifestPath: manifestPath,
	}
}

func engineTestAccount(t *testing.T, cfg Config) account.Account {
	t.Helper()

	acct, err := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365:          credentials.M365{AzureClientID: cfg.ClientID, AzureClientSecret: cfg.ClientSecret},
			AzureTenantID: cfg.TenantID,
		})
	require.NoError(t, err, clues.ToCore(err))

	return acct
}

// mailboxSelector scopes a backup to just cfg.UserID's Exchange Inbox -
// the smallest real workload the 0.3 seeder guarantees exists, so these
// tests exercise a real backup without pulling the user's whole mailbox.
func mailboxSelector(userID string) selectors.Selector {
	seb := selectors.NewExchangeBackup([]string{userID})
	seb.Include(seb.MailFolders([]string{api.MailInbox}, selectors.PrefixMatch()))
	seb.DiscreteOwner = userID

	return seb.Selector
}

func filesystemStorage(t *testing.T, passphrase string) storage.Storage {
	t.Helper()
	return filesystemStorageAt(t, filepath.Join(t.TempDir(), "repo"), passphrase)
}

func filesystemStorageAt(t *testing.T, path, passphrase string) storage.Storage {
	t.Helper()

	st, err := storage.NewStorage(
		storage.ProviderFilesystem,
		&storage.FilesystemConfig{Path: path},
		storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: passphrase}})
	require.NoError(t, err, clues.ToCore(err))

	return st
}

// ---------------------------------------------------------------------------
// Done when #2: opening a repository works with caller-supplied storage
// settings and key, for both a mounted filesystem path and MinIO.
// ---------------------------------------------------------------------------

func (suite *EngineIntegrationSuite) TestOpenRepository_Filesystem() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// Required for this test to pass standalone (go test -run
	// TestEngineIntegrationSuite/TestOpenRepository_Filesystem), not just
	// as part of the whole ./internal/seed/... suite: see
	// api_integration_test.go and smoke_test.go, which call this for the
	// same reason. graph.InitializeConcurrencyLimiter is a sync.Once; if
	// nothing calls it before the first real Graph request in this test
	// binary, the middleware's own zero-value default
	// (concurrencyLimitMiddlewareSingleton, non-nil but with a nil
	// semaphore) gets wired into the pipeline and every Graph call fails
	// with "nil concurrency limiter" - confirmed by running this test
	// standalone before this call was added.
	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	manifestPath := filepath.Join(t.TempDir(), "seed-manifest.json")
	cfg := engineTestConfig(t, manifestPath)
	acct := engineTestAccount(t, cfg)

	repoPath := filepath.Join(t.TempDir(), "repo")
	passphrase := "corso-0.6-engine-fs-" + uuid.NewString()

	repoCfg := engine.RepositoryConfig{
		Storage: filesystemStorageAt(t, repoPath, passphrase),
		Account: acct,
		Options: control.DefaultOptions(),
	}

	repo, err := engine.InitializeRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	id := repo.ID()
	assert.NotEmpty(t, id)

	require.NoError(t, repo.Close(ctx))

	// Re-opening with the exact same caller-supplied storage and key must
	// reach the same repository, with the same ID - not a freshly minted
	// one. (Without passing the fork's own events.RepoIDNotFound sentinel
	// through to repository.New on this path, this used to report a
	// different, meaningless ID every time; see pkg/engine/repository.go.)
	reopened, err := engine.OpenRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, reopened.Close(ctx)) }()

	assert.Equal(t, id, reopened.ID())

	// Initializing again with the SAME correct passphrase is a safe
	// reconnect, not an error - this is the fork's own Initialize
	// behavior (see InitializeRepository's doc comment), confirmed live
	// here rather than assumed.
	reinitialized, err := engine.InitializeRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))
	assert.Equal(t, id, reinitialized.ID())
	require.NoError(t, reinitialized.Close(ctx))

	// Initializing with the WRONG passphrase against the same already-
	// existing repository must fail with ErrRepositoryExists, never
	// silently connect or corrupt the repository.
	wrongPassphraseCfg := repoCfg
	wrongPassphraseCfg.Storage = filesystemStorageAt(t, repoPath, "wrong-"+passphrase)

	_, err = engine.InitializeRepository(ctx, wrongPassphraseCfg)
	assert.ErrorIs(t, err, engine.ErrRepositoryExists)
}

func (suite *EngineIntegrationSuite) TestOpenRepository_MinIO() {
	t := suite.T()

	endpoint := os.Getenv(minioEndpointEnv)
	if len(endpoint) == 0 {
		t.Skipf("%s not set; skipping MinIO-backed test", minioEndpointEnv)
	}

	bucket := os.Getenv(minioBucketEnv)
	require.NotEmpty(t, bucket, minioBucketEnv+" must be set alongside "+minioEndpointEnv)

	ctx, flush := tester.NewContext(t)
	defer flush()

	// See TestOpenRepository_Filesystem's identical call for why this is
	// needed to run standalone.
	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	manifestPath := filepath.Join(t.TempDir(), "seed-manifest.json")
	cfg := engineTestConfig(t, manifestPath)
	acct := engineTestAccount(t, cfg)

	st, err := storage.NewStorage(
		storage.ProviderS3,
		&storage.S3Config{
			AWS: credentials.AWS{
				AccessKey: os.Getenv(minioAccessKeyEnv),
				SecretKey: os.Getenv(minioSecretKeyEnv),
				// MinIO's root user is a static credential, not an
				// STS-issued one; MinIO rejects a session token it did not
				// itself issue (see internal/kopia/minio_roundtrip_test.go).
				SessionToken: "",
			},
			Bucket:      bucket,
			Endpoint:    endpoint,
			Prefix:      "corso-0.6-engine-" + uuid.NewString(),
			DoNotUseTLS: true,
		},
		storage.CommonConfig{Corso: credentials.Corso{CorsoPassphrase: "corso-0.6-engine-minio-" + uuid.NewString()}})
	require.NoError(t, err, clues.ToCore(err))

	repoCfg := engine.RepositoryConfig{
		Storage: st,
		Account: acct,
		Options: control.DefaultOptions(),
	}

	repo, err := engine.InitializeRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	id := repo.ID()
	assert.NotEmpty(t, id)

	require.NoError(t, repo.Close(ctx))

	reopened, err := engine.OpenRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, reopened.Close(ctx)) }()

	assert.Equal(t, id, reopened.ID())
}

// ---------------------------------------------------------------------------
// Done when #3, #4, #5, #6: tags, progress, delete by tag (incl. negative
// case), maintenance.
// ---------------------------------------------------------------------------

func (suite *EngineIntegrationSuite) TestBackup_TagsProgressDeleteMaintenance() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// See TestOpenRepository_Filesystem's identical call for why this is
	// needed to run standalone.
	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	manifestPath := filepath.Join(t.TempDir(), "seed-manifest.json")
	cfg := engineTestConfig(t, manifestPath)
	acct := engineTestAccount(t, cfg)

	creds := account.M365Config{
		M365:          credentials.M365{AzureClientID: cfg.ClientID, AzureClientSecret: cfg.ClientSecret},
		AzureTenantID: cfg.TenantID,
	}

	client, err := api.NewClient(creds, control.DefaultOptions(), count.New())
	require.NoError(t, err, clues.ToCore(err))

	// seed.Run is the same idempotent find-or-create call 0.3 and 0.5
	// already proved safe against this tenant (zero creates on a
	// previously-seeded tenant); this both confirms the seeded user is
	// really there before we spend a real backup on it, and exercises
	// LoadManifest as the brief requires: read back exactly what Run just
	// wrote, rather than trusting Run's in-memory return value alone.
	_, err = Run(ctx, client, cfg, func(line string) { t.Log(line) })
	require.NoError(t, err, clues.ToCore(err))

	manifest, err := LoadManifest(manifestPath)
	require.NoError(t, err, clues.ToCore(err))

	mailItems := manifest.ItemsByWorkload(WorkloadExchangeMail)
	require.NotEmpty(t, mailItems, "manifest must record at least one seeded mail item to back up")

	repoCfg := engine.RepositoryConfig{
		Storage: filesystemStorage(t, "corso-0.6-engine-tags-"+uuid.NewString()),
		Account: acct,
		Options: control.DefaultOptions(),
	}

	repo, err := engine.InitializeRepository(ctx, repoCfg)
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, repo.Close(ctx)) }()

	policyA := "corso-0.6-policy-a-" + uuid.NewString()
	policyB := "corso-0.6-policy-b-" + uuid.NewString()

	var (
		pointA, pointB engine.BackupPoint
		progressCalls  []engine.ProgressSnapshot
		stdoutBytes    []byte
	)

	suite.Run("BackupWithTags", func() {
		// suite.Run (testify) swaps in a subtest-scoped *testing.T before
		// running this closure; capturing a fresh t here, instead of
		// reusing the parent TestBackup_TagsProgressDeleteMaintenance's own
		// t, is required for failures in this subtest to actually be
		// attributed to it. Reusing the outer t (as an earlier version of
		// this file did) meant every subtest's PASS/FAIL was attributed to
		// the parent test instead, and the subtests themselves always
		// printed PASS regardless of what their own assertions found -
		// found in review.
		t := suite.T()

		pointA, err = repo.Backup(ctx, engine.BackupRequest{
			Selector: mailboxSelector(cfg.UserID),
			Tags: map[string]string{
				"k3ep-tenant": cfg.TenantID,
				"k3ep-policy": policyA,
				"k3ep-object": cfg.UserID,
			},
		})
		require.NoError(t, err, clues.ToCore(err))

		assert.NotEmpty(t, pointA.ID)
		assert.Equal(t, cfg.TenantID, pointA.Tags["k3ep-tenant"])
		assert.Equal(t, policyA, pointA.Tags["k3ep-policy"])
		assert.Equal(t, cfg.UserID, pointA.Tags["k3ep-object"])
		// the fork's own bookkeeping tag must still be present alongside
		// the caller's tags, not replaced by them.
		assert.NotEmpty(t, pointA.Tags["service"])
	})

	suite.Run("ProgressCallbackInvokedAndNoStdout", func() {
		t := suite.T()

		stdoutBytes = captureStdout(t, func() {
			var mu sync.Mutex

			pointB, err = repo.Backup(ctx, engine.BackupRequest{
				Selector: mailboxSelector(cfg.UserID),
				Tags: map[string]string{
					"k3ep-tenant": cfg.TenantID,
					"k3ep-policy": policyB,
					"k3ep-object": cfg.UserID,
				},
				Progress: func(p engine.ProgressSnapshot) {
					mu.Lock()
					defer mu.Unlock()

					progressCalls = append(progressCalls, p)
				},
			})
		})
		require.NoError(t, err, clues.ToCore(err))

		require.NotEmpty(t, progressCalls, "progress callback must be invoked during a real backup")
		assert.True(t, progressCalls[len(progressCalls)-1].Done,
			"the final progress call must have Done set")
		assert.Empty(t, stdoutBytes, "pkg/engine must never write to stdout")
	})

	suite.Run("ListBackupPointsByTag", func() {
		t := suite.T()

		onlyA, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-policy": policyA})
		require.NoError(t, err, clues.ToCore(err))
		require.Len(t, onlyA, 1)
		assert.Equal(t, pointA.ID, onlyA[0].ID)

		onlyB, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-policy": policyB})
		require.NoError(t, err, clues.ToCore(err))
		require.Len(t, onlyB, 1)
		assert.Equal(t, pointB.ID, onlyB[0].ID)

		both, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-object": cfg.UserID})
		require.NoError(t, err, clues.ToCore(err))
		assert.Len(t, both, 2)

		// Done-when #2/#9's documented behavior: a nil/empty tag filter
		// returns every backup point in the repository, not nothing - this
		// package has no notion of "my tenant" to default to. Proven live,
		// not just asserted in a doc comment.
		everything, err := repo.ListBackupPoints(ctx, nil)
		require.NoError(t, err, clues.ToCore(err))
		assert.GreaterOrEqual(t, len(everything), 2, "nil tags must return at least both points created above")
	})

	suite.Run("DeleteBackupPointsByTag", func() {
		t := suite.T()

		deletedIDs, err := repo.DeleteBackupPoints(ctx, map[string]string{"k3ep-policy": policyA})
		require.NoError(t, err, clues.ToCore(err))
		assert.Equal(t, []string{pointA.ID}, deletedIDs,
			"DeleteBackupPoints must return the deleted point's own ID, not just a count")

		// Positive case: pointA is gone.
		gone, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-policy": policyA})
		require.NoError(t, err, clues.ToCore(err))
		assert.Empty(t, gone)

		// Negative case: pointB, which does not carry policyA, must be
		// completely unaffected by a delete scoped to policyA.
		stillThere, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-policy": policyB})
		require.NoError(t, err, clues.ToCore(err))
		require.Len(t, stillThere, 1)
		assert.Equal(t, pointB.ID, stillThere[0].ID)
	})

	suite.Run("MaintenanceKeepsRepositoryReadable", func() {
		t := suite.T()

		err := repo.RunMaintenance(ctx, engine.MaintenanceOptions{Force: true})
		require.NoError(t, err, clues.ToCore(err))

		// The repository must still be fully readable afterwards: pointB,
		// which was never deleted, must still be there.
		stillThere, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-policy": policyB})
		require.NoError(t, err, clues.ToCore(err))
		require.Len(t, stillThere, 1)
		assert.Equal(t, pointB.ID, stillThere[0].ID)
	})
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Used to prove pkg/engine's progress reporting
// never writes to stdout - the brief's exact wording for Done-when #4.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stdout
	os.Stdout = w

	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	return out
}
