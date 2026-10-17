package seed

import (
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/errs/core"
)

type UpsertUnitSuite struct {
	tester.Suite
}

func TestUpsertUnitSuite(t *testing.T) {
	suite.Run(t, &UpsertUnitSuite{Suite: tester.NewUnitSuite(t)})
}

// fakeBackend stands in for Graph: a tiny in-memory store that FindOrCreate
// drives exactly the way the real seeder drives api.Client. The whole point
// is to prove the idempotency guarantee without a network call.
type fakeBackend struct {
	byName    map[string]string // name -> id
	createLog []string          // every name create() was actually invoked with
	nextID    int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{byName: map[string]string{}}
}

func (f *fakeBackend) find(name string) (string, bool, error) {
	id, ok := f.byName[name]
	return id, ok, nil
}

func (f *fakeBackend) create(name string) (string, error) {
	f.createLog = append(f.createLog, name)
	f.nextID++
	id := name + "-id"
	f.byName[name] = id

	return id, nil
}

func (suite *UpsertUnitSuite) TestFindOrCreate_FirstCallCreates() {
	t := suite.T()

	be := newFakeBackend()

	id, created, err := FindOrCreate(
		func() (string, bool, error) { return be.find("k3ep-seed-mail") },
		func() (string, error) { return be.create("k3ep-seed-mail") })

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "k3ep-seed-mail-id", id)
	assert.Equal(t, []string{"k3ep-seed-mail"}, be.createLog)
}

// TestFindOrCreate_SecondCallReusesExisting is the direct proof, independent
// of any real tenant, of the repeatability requirement in this chunk's
// Done-when #2: running the same FindOrCreate call again, once the backend
// can see what the first call created, must not create a second time, and
// must return the same ID.
func (suite *UpsertUnitSuite) TestFindOrCreate_SecondCallReusesExisting() {
	t := suite.T()

	be := newFakeBackend()

	seedOnce := func() (string, bool, error) {
		id, created, err := FindOrCreate(
			func() (string, bool, error) { return be.find("k3ep-seed-mail") },
			func() (string, error) { return be.create("k3ep-seed-mail") })

		return id, created, err
	}

	id1, created1, err := seedOnce()
	require.NoError(t, err)
	assert.True(t, created1)

	id2, created2, err := seedOnce()
	require.NoError(t, err)
	assert.False(t, created2, "second run must not create a duplicate")
	assert.Equal(t, id1, id2, "second run must return the same stable ID")

	assert.Len(t, be.createLog, 1, "create must have been invoked exactly once across two runs")
}

func (suite *UpsertUnitSuite) TestFindOrCreate_FindErrorNeverCalledCreate() {
	t := suite.T()

	be := newFakeBackend()
	wantErr := clues.New("transient failure")

	_, created, err := FindOrCreate(
		func() (string, bool, error) { return "", false, wantErr },
		func() (string, error) { return be.create("should-not-run") })

	require.Error(t, err)
	assert.False(t, created)
	assert.Empty(t, be.createLog, "create must not run when find fails")
}

func (suite *UpsertUnitSuite) TestFindOrCreate_CreateErrorPropagates() {
	t := suite.T()

	wantErr := clues.New("create failed")

	_, created, err := FindOrCreate(
		func() (string, bool, error) { return "", false, nil },
		func() (string, error) { return "", wantErr })

	require.Error(t, err)
	assert.False(t, created)
}

func (suite *UpsertUnitSuite) TestContainerNotFound() {
	t := suite.T()

	assert.False(t, ContainerNotFound(nil))
	assert.True(t, ContainerNotFound(clues.New("container not found")))
	assert.True(t, ContainerNotFound(clues.Stack(core.ErrNotFound, clues.New("404"))))
	assert.False(t, ContainerNotFound(clues.New("permission denied")))
}

func (suite *UpsertUnitSuite) TestAlreadyExists() {
	t := suite.T()

	assert.False(t, AlreadyExists(nil))
	assert.False(t, AlreadyExists(clues.New("some other error")))
	assert.True(t, AlreadyExists(clues.Stack(core.ErrAlreadyExists, clues.New("409"))))
}
