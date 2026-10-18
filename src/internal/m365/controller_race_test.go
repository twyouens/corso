package m365

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inMock "github.com/twyouens/corso/src/internal/common/idname/mock"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// TestEnsureResourceHandler_ConcurrentWithPopulate is chunk 0.6's live
// review finding: EnsureResourceHandler (added by this chunk, called on
// every pkg/repository connectToM365 reuse) writes Controller.resourceHandler
// with no synchronization, while PopulateProtectedResourceIDAndName reads
// it - exactly the shape two concurrent pkg/engine.Backup calls on one
// *engine.Repository produce, since both share one underlying
// *m365.Controller. Confirmed by both the standard and security reviewers
// independently.
//
// ins is populated so GetResourceIDAndNameFrom resolves purely from the
// cache, with no real Graph call (see its own doc comment): this test
// needs no network access or live credentials, only -race.
func TestEnsureResourceHandler_ConcurrentWithPopulate(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	ctrl := &Controller{AC: api.Client{}}

	// Seed a valid initial handler synchronously, before the two
	// goroutines start: without this, the reader goroutine can
	// legitimately observe resourceHandler's zero value (a true nil
	// interface, nobody has called EnsureResourceHandler yet) before the
	// writer goroutine's first iteration has run - a real but uninteresting
	// race on goroutine scheduling order, not the bug this test is for.
	ctrl.EnsureResourceHandler(path.ExchangeService)

	ins := inMock.NewCache(
		map[string]string{"owner-id": "owner-name"},
		map[string]string{"owner-name": "owner-id"})

	services := []path.ServiceType{
		path.ExchangeService,
		path.OneDriveService,
		path.SharePointService,
		path.GroupsService,
	}

	var wg sync.WaitGroup

	// Writer: repeatedly re-derives the resource handler for a rotating
	// set of services, exactly as pkg/repository's connectToM365 does on
	// every ConnectDataProvider call.
	wg.Add(1)

	go func() {
		defer wg.Done()

		for i := 0; i < 500; i++ {
			ctrl.EnsureResourceHandler(services[i%len(services)])
		}
	}()

	// Reader: repeatedly resolves a resource owner, exactly as a backup
	// does at the start of NewBackupWithLookup.
	wg.Add(1)

	go func() {
		defer wg.Done()

		for i := 0; i < 500; i++ {
			_, err := ctrl.PopulateProtectedResourceIDAndName(ctx, "owner-id", ins)
			assert.NoError(t, err)
		}
	}()

	wg.Wait()
}

// TestSetResourceHandler_UnrecognizedServiceIsNilInterface proves the typed-
// nil fix: a service setResourceHandler does not recognize must leave
// Controller.resourceHandler a true nil interface (so
// PopulateProtectedResourceIDAndName's `rh == nil` check fires and returns
// ErrNoResourceLookup), not a non-nil interface wrapping a nil
// *resourceGetter (which would instead panic inside
// GetResourceIDAndNameFrom on a nil pointer receiver).
func TestSetResourceHandler_UnrecognizedServiceIsNilInterface(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	ctrl := &Controller{AC: api.Client{}}

	ctrl.EnsureResourceHandler(path.UnknownService)

	_, err := ctrl.PopulateProtectedResourceIDAndName(ctx, "owner-id", nil)
	require.ErrorIs(t, err, ErrNoResourceLookup)
}
