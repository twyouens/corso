package kopia

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/alcionai/clues"
	"github.com/kopia/kopia/repo/blob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/common/ptr"
	strTD "github.com/twyouens/corso/src/internal/common/str/testdata"
	"github.com/twyouens/corso/src/internal/data"
	dataMock "github.com/twyouens/corso/src/internal/data/mock"
	exchMock "github.com/twyouens/corso/src/internal/m365/service/exchange/mock"
	istats "github.com/twyouens/corso/src/internal/stats"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/backup/identity"
	"github.com/twyouens/corso/src/pkg/control/repository"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/fault"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/storage"
	storeTD "github.com/twyouens/corso/src/pkg/storage/testdata"
)

// Env vars that point this test at a locally running, standalone,
// S3-compatible MinIO instance. This proves chunk 0.4's "done when" #6: a
// backup-and-restore round trip over the S3 path, through upgraded upstream
// Kopia's repo/blob/s3 package, against a real S3-compatible server rather
// than mocks.
//
// The test skips cleanly when CORSO_TEST_MINIO_ENDPOINT is unset, the same
// pattern every other integration suite in this package uses for external
// infrastructure it needs (tester.NewIntegrationSuite / RunOnAny). It is not
// wired through scripts/with-test-tenant.sh because it needs no Microsoft
// 365 test-tenant credentials at all -- only a disposable local MinIO
// container -- so running it does not depend on the test tenant that chunk
// 0.3 has not provisioned yet.
//
// See the chunk 0.4 pull request description for the exact commands used to
// start the container and create the bucket.
const (
	minioEndpointEnv  = "CORSO_TEST_MINIO_ENDPOINT"
	minioBucketEnv    = "CORSO_TEST_MINIO_BUCKET"
	minioAccessKeyEnv = "CORSO_TEST_MINIO_ACCESS_KEY"
	minioSecretKeyEnv = "CORSO_TEST_MINIO_SECRET_KEY"

	// minioLockBucketEnv names a second bucket, created with S3 Object
	// Lock (and the versioning it requires) enabled. A repository with a
	// retention mode set writes its "kopia.blobcfg" and other blobs with
	// S3 object-lock headers that a bucket without a lock configuration
	// rejects outright ("Bucket is missing ObjectLockConfiguration"),
	// confirmed empirically against the chunk 0.4 MinIO container. The
	// plain round-trip test above intentionally does not need this.
	minioLockBucketEnv = "CORSO_TEST_MINIO_LOCK_BUCKET"
)

// requireMinIO skips the calling test unless CORSO_TEST_MINIO_ENDPOINT is
// set, and otherwise builds a storage.Storage pointed at bucketEnv (read
// from the named env var) with a fresh, per-test prefix so tests sharing
// the same bucket don't collide.
func requireMinIO(t *testing.T, bucketEnv string) storage.Storage {
	endpoint := os.Getenv(minioEndpointEnv)
	if len(endpoint) == 0 {
		t.Skipf("%s not set; skipping MinIO-backed test", minioEndpointEnv)
	}

	bucket := os.Getenv(bucketEnv)
	require.NotEmpty(t, bucket, bucketEnv+" must be set alongside "+minioEndpointEnv)

	st, err := storage.NewStorage(
		storage.ProviderS3,
		&storage.S3Config{
			AWS: credentials.AWS{
				AccessKey: os.Getenv(minioAccessKeyEnv),
				SecretKey: os.Getenv(minioSecretKeyEnv),
				// Deliberately left empty: MinIO's root user is a static
				// credential, not an STS-issued temporary one, and MinIO
				// rejects requests carrying a security token it did not
				// itself issue (confirmed empirically against the chunk
				// 0.4 MinIO container; see the pull request). S3Config's
				// own validation does not require this field.
				SessionToken: "",
			},
			Bucket: bucket,
			// MinIO's default self-signed/no-TLS local setup; STO-1
			// destinations choose per-destination, this test only proves
			// the code path works against a real S3-compatible endpoint.
			Endpoint:    endpoint,
			Prefix:      "corso-0.4-roundtrip-" + strTD.NewHashForRepoConfigName(),
			DoNotUseTLS: true,
		},
		storage.CommonConfig{
			Corso:       storeTD.GetAndInsertCorso("corso-0.4-minio-roundtrip-passphrase"),
			KopiaCfgDir: t.TempDir(),
		})
	require.NoError(t, err, clues.ToCore(err))

	return st
}

func TestKopiaMinIORoundTripIntegration(t *testing.T) {
	st := requireMinIO(t, minioBucketEnv)

	ctx, flush := tester.NewContext(t)
	defer flush()

	repoNameHash := strTD.NewHashForRepoConfigName()

	k := NewConn(st)

	err := k.Initialize(ctx, repository.Options{}, repository.Retention{}, repoNameHash)
	require.NoError(t, err, "initializing repo on MinIO: %v", clues.ToCore(err))

	defer func() {
		assert.NoError(t, k.Close(ctx))
	}()

	w := &Wrapper{k}

	testPath, err := path.Build(
		testTenant,
		testUser,
		path.ExchangeService,
		path.EmailCategory,
		false,
		testInboxDir)
	require.NoError(t, err, clues.ToCore(err))

	data1 := []byte("corso 0.4 minio round trip: file one")
	data2 := []byte("corso 0.4 minio round trip: file two, slightly different content")

	loc := path.Builder{}.Append(testPath.Folders()...)
	collection := &dataMock.Collection{Path: testPath, Loc: loc}
	collection.ItemData = append(
		collection.ItemData,
		&dataMock.Item{
			ItemID:   "item-one",
			Reader:   io.NopCloser(bytes.NewReader(data1)),
			ItemInfo: exchMock.StubMailInfo(),
		},
		&dataMock.Item{
			ItemID:   "item-two",
			Reader:   io.NopCloser(bytes.NewReader(data2)),
			ItemInfo: exchMock.StubMailInfo(),
		})

	collections := []data.BackupCollection{dataMock.NewVersionedBackupCollection(t, collection)}

	reason := identity.NewReason(testTenant, testUser, path.ExchangeService, path.EmailCategory)
	counter := count.New()

	stats, _, _, err := w.ConsumeBackupCollections(
		ctx,
		[]identity.Reasoner{reason},
		nil,
		collections,
		nil,
		nil,
		false,
		counter,
		fault.New(true))
	require.NoError(t, err, "backing up to MinIO: %v", clues.ToCore(err))
	require.Zero(t, stats.ErrorCount)
	require.False(t, stats.Incomplete)
	require.Equal(t, 2, stats.TotalFileCount)

	item1Path, err := testPath.AppendItem("item-one")
	require.NoError(t, err, clues.ToCore(err))

	item2Path, err := testPath.AppendItem("item-two")
	require.NoError(t, err, clues.ToCore(err))

	expected := map[string][]byte{
		item1Path.String(): data1,
		item2Path.String(): data2,
	}

	ic := istats.ByteCounter{Counter: counter.AdderFor(count.PersistedUploadedBytes)}

	result, err := w.ProduceRestoreCollections(
		ctx,
		stats.SnapshotID,
		toRestorePaths(t, item1Path, item2Path),
		&ic,
		fault.New(true))
	require.NoError(t, err, "restoring from MinIO: %v", clues.ToCore(err))

	testForFiles(t, ctx, expected, result)
}

// TestKopiaMinIORetentionIntegration proves chunk 0.4's "done when" #8 (the
// retention/object-lock path) against a real upstream Kopia repository
// rather than only the opts.go unit tests, which never exercise the actual
// persistence call (conn.go's persistRetentionConfigs ->
// format.Manager.SetParameters). While researching the upgrade, current
// upstream Kopia's format.BlobStorageConfiguration gained a Validate()
// method (repo/format/blobcfg_blob.go) that format.Manager.SetParameters
// now calls on every retention update
// (repo/format/format_manager.go:472). It rejects:
//  1. a retention period set without a retention mode, or vice versa
//     (they must both be empty or both be set), and
//  2. a non-zero retention period under 24 hours.
//
// Both rules are already implicitly covered by this file's sibling,
// RetentionIntegrationSuite.TestSetRetentionParameters (the "UpdateDuration"
// case expects an error for exactly rule 1, and the suite's shortest
// configured duration is 48h, comfortably clear of rule 2) -- but that
// suite only runs against a real AWS S3 bucket this environment does not
// have. This test reruns the same two cases against the local MinIO
// container to confirm the behaviour survives the upgrade without needing
// AWS.
func TestKopiaMinIORetentionIntegration(t *testing.T) {
	st := requireMinIO(t, minioLockBucketEnv)

	ctx, flush := tester.NewContext(t)
	defer flush()

	repoNameHash := strTD.NewHashForRepoConfigName()

	k := NewConn(st)

	err := k.Initialize(ctx, repository.Options{}, repository.Retention{}, repoNameHash)
	require.NoError(t, err, "initializing repo on MinIO: %v", clues.ToCore(err))

	defer func() {
		assert.NoError(t, k.Close(ctx))
	}()

	w := &Wrapper{k}

	// Rule 1: a duration with no mode is rejected by
	// format.BlobStorageConfiguration.Validate(), even though nothing in
	// opts.go itself objects (setBlobConfigDuration sets the period
	// unconditionally; setBlobConfigMode is a no-op when mode is nil).
	err = w.SetRetentionParameters(ctx, repository.Retention{
		Duration: ptr.To(time.Hour * 48),
	})
	assert.Error(t, err, "setting duration without a mode should be rejected")

	checkRetentionParams(t, ctx, k, blob.RetentionMode(""), 0, assert.False)

	// Rule 2 (the new one found while auditing the upgrade): a non-zero
	// period under the 24h minimum is rejected too. 1 hour is comfortably
	// under the minimum and would previously have had no such floor to
	// check against in a config shape this library has not called
	// Validate() against before.
	err = w.SetRetentionParameters(ctx, repository.Retention{
		Mode:     ptr.To(repository.GovernanceRetention),
		Duration: ptr.To(time.Hour),
	})
	assert.Error(t, err, "setting a sub-24h retention period should be rejected")

	checkRetentionParams(t, ctx, k, blob.RetentionMode(""), 0, assert.False)

	// Mode and a period clear of both rules succeeds and persists exactly
	// as requested, same as RetentionIntegrationSuite.TestSetRetentionParameters's
	// "UpdateModeAndDuration_Governance" case.
	err = w.SetRetentionParameters(ctx, repository.Retention{
		Mode:     ptr.To(repository.GovernanceRetention),
		Duration: ptr.To(time.Hour * 48),
	})
	require.NoError(t, err, "setting a valid mode and duration: %v", clues.ToCore(err))

	checkRetentionParams(t, ctx, k, blob.Governance, time.Hour*48, assert.False)
}
