package seed

import (
	"path/filepath"
	"testing"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/engine"
)

// This file is chunk 0.10's live counterpart to the per-client Graph
// concurrency limiter's unit tests.
//
// The unit tests in pkg/services/m365/api/graph measure the limiter at the
// semaphore, against a loopback server; they prove the capacity is per
// client and is honoured. What they cannot show is that a *narrow* capacity
// still works against real Microsoft Graph end to end, through the real
// adapter, the real auth provider and the real rate limiters - and that is
// the half worth proving live, because the process-global version this
// replaced made capacity 1 reachable for an entire worker process by
// accident, and nothing in this fork ever exercised it deliberately.
//
// Lives in internal/seed, not pkg/engine, for the same reason the engine and
// browse integration suites do (see engine_integration_test.go): `make
// test-integration` is scoped to ./internal/seed/....
//
// Read-only with respect to the test tenant: it backs up the already-seeded
// user's mailbox and creates nothing at the directory level.

type PerClientConcurrencyIntegrationSuite struct {
	tester.Suite
}

func TestPerClientConcurrencyIntegrationSuite(t *testing.T) {
	suite.Run(t, &PerClientConcurrencyIntegrationSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{RequiredEnvVars},
			tester.CorsoNightlyTests),
	})
}

// TestTwoRepositoriesWithDifferentParallelism opens two repositories in one
// process with different Options.Parallelism.ItemFetch and runs a real
// backup through each.
//
// The narrow repository is opened first on purpose. Under the process-global
// limiter this replaced, the first repository opened in the process latched
// the capacity for every Graph client built afterwards, so this order is the
// one that used to clamp the second repository to a single in-flight Graph
// request. Both backups now succeed with their own capacity, and the
// capacity-1 path - previously reachable but never deliberately exercised -
// is confirmed to work against real Graph rather than merely against a
// loopback server.
func (suite *PerClientConcurrencyIntegrationSuite) TestTwoRepositoriesWithDifferentParallelism() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	manifestPath := filepath.Join(t.TempDir(), "seed-manifest.json")
	cfg := engineTestConfig(t, manifestPath)
	acct := engineTestAccount(t, cfg)

	narrowOpts := control.DefaultOptions()
	narrowOpts.Parallelism.ItemFetch = 1

	wideOpts := control.DefaultOptions()
	wideOpts.Parallelism.ItemFetch = 4

	narrowRepo, err := engine.InitializeRepository(ctx, engine.RepositoryConfig{
		Storage: filesystemStorage(t, "corso-0.10-concurrency-narrow-"+uuid.NewString()),
		Account: acct,
		Options: narrowOpts,
	})
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, narrowRepo.Close(ctx)) }()

	wideRepo, err := engine.InitializeRepository(ctx, engine.RepositoryConfig{
		Storage: filesystemStorage(t, "corso-0.10-concurrency-wide-"+uuid.NewString()),
		Account: acct,
		Options: wideOpts,
	})
	require.NoError(t, err, clues.ToCore(err))

	defer func() { assert.NoError(t, wideRepo.Close(ctx)) }()

	table := []struct {
		name string
		repo *engine.Repository
	}{
		{name: "narrow repository, ItemFetch 1, opened first", repo: narrowRepo},
		{name: "wide repository, ItemFetch 4, opened second", repo: wideRepo},
	}

	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			point, err := test.repo.Backup(ctx, engine.BackupRequest{
				Selector: mailboxSelector(cfg.UserID),
				Tags: map[string]string{
					"k3ep-tenant": cfg.TenantID,
					"k3ep-policy": "corso-0.10-concurrency-" + uuid.NewString(),
					"k3ep-object": cfg.UserID,
				},
			})
			require.NoError(t, err, clues.ToCore(err))

			assert.NotEmpty(t, point.ID, "backup point id")
			assert.Positive(t, point.ItemsRead, "items read from real Graph")

			t.Logf("backup point %s read %d items", point.ID, point.ItemsRead)
		})
	}
}
