package seed

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
)

type ManifestUnitSuite struct {
	tester.Suite
}

func TestManifestUnitSuite(t *testing.T) {
	suite.Run(t, &ManifestUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func sampleItems() []ManifestItem {
	return []ManifestItem{
		{
			Workload:   WorkloadOneDrive,
			Kind:       "file",
			ID:         "item-2",
			Name:       "b.txt",
			FolderPath: Prefix,
			SizeBytes:  42,
			ResourceID: "drive-1",
		},
		{
			Workload:   WorkloadExchangeMail,
			Kind:       "message",
			ID:         "item-1",
			Name:       "hello",
			FolderPath: Prefix,
			ResourceID: "user-1",
		},
		{
			Workload:   WorkloadOneDrive,
			Kind:       "file",
			ID:         "item-3",
			Name:       "a.txt",
			FolderPath: Prefix,
			SizeBytes:  7,
			ResourceID: "drive-1",
		},
	}
}

// TestNewManifest_DeterministicOrdering is the unit-test proof for Done-when
// #2 (repeatability): the same logical items, fed in a different input
// order, must produce the same manifest, because later chunks diff two
// manifests byte-for-byte to prove a second seeder run changed nothing.
func (suite *ManifestUnitSuite) TestNewManifest_DeterministicOrdering() {
	t := suite.T()

	items := sampleItems()

	shuffled := []ManifestItem{items[2], items[0], items[1]}

	m1 := NewManifest("tenant-a", items)
	m2 := NewManifest("tenant-a", shuffled)

	require.Len(t, m1.Items, 3)
	assert.Equal(t, m1.Items, m2.Items)

	// and the ordering itself groups by workload first.
	assert.Equal(t, WorkloadExchangeMail, m1.Items[0].Workload)
	assert.Equal(t, WorkloadOneDrive, m1.Items[1].Workload)
	assert.Equal(t, WorkloadOneDrive, m1.Items[2].Workload)
	// within onedrive, "a.txt" sorts before "b.txt".
	assert.Equal(t, "a.txt", m1.Items[1].Name)
	assert.Equal(t, "b.txt", m1.Items[2].Name)
}

func (suite *ManifestUnitSuite) TestManifest_WriteAndLoadRoundTrip() {
	t := suite.T()

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "seed-manifest.json")

	want := NewManifest("tenant-b", sampleItems())

	require.NoError(t, want.Write(path))

	got, err := LoadManifest(path)
	require.NoError(t, err)

	// GeneratedAt round-trips through JSON (RFC3339Nano) losslessly for UTC
	// times truncated by json; compare with Equal on the UTC/truncated form.
	assert.True(t, want.GeneratedAt.Equal(got.GeneratedAt))
	assert.Equal(t, want.TenantID, got.TenantID)
	assert.Equal(t, want.Prefix, got.Prefix)
	assert.Equal(t, want.SchemaVersion, got.SchemaVersion)
	assert.Equal(t, want.Items, got.Items)
}

func (suite *ManifestUnitSuite) TestManifest_WriteTwiceIsIdempotentOnDisk() {
	t := suite.T()

	dir := t.TempDir()
	path := filepath.Join(dir, "seed-manifest.json")

	m := NewManifest("tenant-c", sampleItems())

	require.NoError(t, m.Write(path))
	first, err := LoadManifest(path)
	require.NoError(t, err)

	// simulate a second seeder run some time later producing the exact same
	// logical items: only GeneratedAt should differ.
	m2 := m
	m2.GeneratedAt = m.GeneratedAt.Add(time.Hour)
	require.NoError(t, m2.Write(path))

	second, err := LoadManifest(path)
	require.NoError(t, err)

	assert.Equal(t, first.Items, second.Items)
	assert.Equal(t, first.TenantID, second.TenantID)
	assert.NotEqual(t, first.GeneratedAt, second.GeneratedAt)
}

func (suite *ManifestUnitSuite) TestManifest_ItemsByWorkload() {
	t := suite.T()

	m := NewManifest("tenant-d", sampleItems())

	onedrive := m.ItemsByWorkload(WorkloadOneDrive)
	assert.Len(t, onedrive, 2)

	mail := m.ItemsByWorkload(WorkloadExchangeMail)
	require.Len(t, mail, 1)
	assert.Equal(t, "hello", mail[0].Name)

	assert.Empty(t, m.ItemsByWorkload(WorkloadSharePointList))
}

func (suite *ManifestUnitSuite) TestLoadManifest_MissingFileErrors() {
	t := suite.T()

	_, err := LoadManifest(filepath.Join(t.TempDir(), "does-not-exist.json"))
	require.Error(t, err)
}
