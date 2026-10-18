package m365

import (
	"context"
	"runtime/trace"
	"sync"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/common/idname"
	"github.com/twyouens/corso/src/internal/data"
	"github.com/twyouens/corso/src/internal/m365/resource"
	"github.com/twyouens/corso/src/internal/m365/support"
	"github.com/twyouens/corso/src/internal/operations/inject"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/backup/details"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/errs/core"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

var ErrNoResourceLookup = clues.New("missing resource lookup client")

// must comply with BackupProducer and RestoreConsumer
var (
	_ inject.BackupProducer   = &Controller{}
	_ inject.ToServiceHandler = &Controller{}
)

// Controller is a struct used to wrap the GraphServiceClient and
// GraphRequestAdapter from the msgraph-sdk-go. Additional fields are for
// bookkeeping and interfacing with other component.
type Controller struct {
	AC api.Client

	tenant      string
	credentials account.M365Config

	// resourceHandler's every read and write is guarded by mu: a
	// Controller is created once per repository connection and reused
	// across every later ConnectDataProvider call on that connection
	// (pkg/repository's connectToM365), each of which may run
	// concurrently with another on a different goroutine - two concurrent
	// pkg/engine.Backup calls on one *engine.Repository is exactly this
	// shape. Before this lock was added, EnsureResourceHandler writing
	// resourceHandler while PopulateProtectedResourceIDAndName read it was
	// a confirmed data race (go test -race), and the race's real-world
	// failure mode is not just a torn read: EnsureResourceHandler could
	// legitimately change resourceHandler mid-operation and resolve a
	// SharePoint site through the previous operation's Users getter.
	resourceHandler idname.GetResourceIDAndNamer
	// IDNameLookup's one write (inside PopulateProtectedResourceIDAndName)
	// is guarded by mu too, but it has no concurrent production reader to
	// race against: ProduceBackupCollections (internal/m365/backup.go),
	// the one call site that used to read this field, now builds its own
	// value from BackupProducerConfig.ProtectedResource instead - the
	// same resource this field is populated from for a single backup
	// operation, but without touching shared state. A -race run
	// (TestControllerConcurrency_ProduceBackupCollectionsAndPopulate,
	// controller_race_test.go) found exactly this read racing against
	// PopulateProtectedResourceIDAndName's write before that change; it
	// is kept as a permanent regression test for the write side even
	// though the read it originally caught is gone.
	//
	// Maps of resource owner ids to names, and names to ids. Not
	// guaranteed to be populated, only here as a post-population
	// reference for processes that choose to populate the values.
	IDNameLookup idname.Cacher

	// wg is used to track completion of tasks
	wg     *sync.WaitGroup
	region *trace.Region

	// mu guards every read and write of resourceHandler (above) and
	// status (below) - status is written under it by UpdateStatus and
	// read under it by Wait/Status/PrintableStatus, all of which can run
	// concurrently with UpdateStatus on a Controller shared across
	// concurrent operations, same as resourceHandler. It does not, on its
	// own, guard wg: Wait()'s own replacement of wg with a fresh
	// *sync.WaitGroup is a pre-existing concern this fix does not widen
	// its scope to cover.
	mu     sync.Mutex
	status support.ControllerOperationStatus // contains the status of the last run status

	// backupDriveIDNames is populated on restore and export.  It maps
	// the backup's drive names to their id. Primarily for use when
	// creating or looking up a new drive.
	backupDriveIDNames idname.CacheBuilder

	// backupSiteIDWebURL is populated on restore and export. It maps
	// the backup's site names to their id. Primarily for use in
	// exports for groups.
	backupSiteIDWebURL idname.CacheBuilder
}

func NewController(
	ctx context.Context,
	acct account.Account,
	pst path.ServiceType,
	co control.Options,
	counter *count.Bus,
) (*Controller, error) {
	graph.InitializeConcurrencyLimiter(ctx, pst == path.ExchangeService, co.Parallelism.ItemFetch)

	creds, err := acct.M365Config()
	if err != nil {
		return nil, clues.WrapWC(ctx, err, "retrieving m365 account configuration")
	}

	ac, err := api.NewClient(creds, co, counter)
	if err != nil {
		return nil, clues.WrapWC(ctx, err, "creating api client")
	}

	ctrl := Controller{
		AC:           ac,
		IDNameLookup: idname.NewCache(nil),

		credentials:        creds,
		tenant:             acct.ID(),
		wg:                 &sync.WaitGroup{},
		backupDriveIDNames: idname.NewCache(nil),
		backupSiteIDWebURL: idname.NewCache(nil),
	}

	// TODO: remove in favor of calling setResourceHandler in the newServiceHandler once
	// all operations types are populated.  When we do that, we can also remove pst from
	// the func params in this call.
	ctrl.setResourceHandler(pst)

	return &ctrl, nil
}

func (ctrl *Controller) VerifyAccess(ctx context.Context) error {
	return ctrl.AC.Access().GetToken(ctx)
}

// EnsureResourceHandler re-derives this controller's resource lookup
// handler for serviceInOperation, overwriting whatever it was set to
// before. Safe to call concurrently with itself and with
// PopulateProtectedResourceIDAndName/NewServiceHandler on the same
// Controller - see mu's doc comment on the struct.
//
// A Controller is created once per repository connection and, in
// pkg/repository's connectToM365, reused across every later call to
// ConnectDataProvider on that same repository - which happens once per
// backup, restore or export, each potentially naming a different service,
// and, in k3ep's long-lived-worker shape, potentially concurrently with
// another such call on the same connection. Without this, a Controller
// first created for one service (or for path.UnknownService, which chunk
// 0.6's pkg/engine passes while opening a repository before any backup has
// chosen a service yet) keeps that service's resource handler forever, and
// PopulateProtectedResourceIDAndName panics on a nil handler the first time
// a later call asks for a different service. Confirmed live against the
// test tenant: pkg/engine opens a repository (which connects a provider
// for path.UnknownService, since no service is known yet) and then calls
// Backup with an Exchange selector, which panicked on exactly this before
// this fix.
func (ctrl *Controller) EnsureResourceHandler(serviceInOperation path.ServiceType) {
	ctrl.setResourceHandler(serviceInOperation)
}

// setResourceHandler derives the resource lookup handler for
// serviceInOperation, stores it, and also returns it, so a caller that
// needs the value right away (NewServiceHandler) does not have to take a
// second, separate, racy read of the shared field.
//
// rh is declared with the idname.GetResourceIDAndNamer interface type, not
// the concrete *resourceGetter pointer type, deliberately: assigning a nil
// *resourceGetter to an interface-typed field produces a non-nil interface
// holding a nil concrete value (a classic Go footgun - the interface's type
// word is set even though its data word is nil), so
// `ctrl.resourceHandler == nil` in PopulateProtectedResourceIDAndName would
// never be true for an unrecognized service, and the method would instead
// panic inside GetResourceIDAndNameFrom on a nil pointer receiver rather
// than returning the documented ErrNoResourceLookup. Declaring rh as the
// interface type keeps its zero value a true nil interface whenever no
// switch case matches.
func (ctrl *Controller) setResourceHandler(
	serviceInOperation path.ServiceType,
) idname.GetResourceIDAndNamer {
	var rh idname.GetResourceIDAndNamer

	switch serviceInOperation {
	case path.ExchangeService, path.OneDriveService, path.TeamsChatsService:
		rh = &resourceGetter{
			enum:   resource.Users,
			getter: ctrl.AC.Users(),
		}
	case path.GroupsService:
		rh = &resourceGetter{
			enum:   resource.Groups,
			getter: ctrl.AC.Groups(),
		}
	case path.SharePointService:
		rh = &resourceGetter{
			enum:   resource.Sites,
			getter: ctrl.AC.Sites(),
		}
	}

	ctrl.mu.Lock()
	ctrl.resourceHandler = rh
	ctrl.mu.Unlock()

	return rh
}

// ---------------------------------------------------------------------------
// Processing Status
// ---------------------------------------------------------------------------

// AwaitStatus waits for all tasks to complete and then returns status
func (ctrl *Controller) Wait() *data.CollectionStats {
	defer func() {
		if ctrl.region != nil {
			ctrl.region.End()
			ctrl.region = nil
		}
	}()
	ctrl.wg.Wait()

	// Guards the read of status below and its own reset, the same as
	// UpdateStatus's write - this method both reads and resets status,
	// so it needs the lock for both, not just the read.
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	// clean up and reset statefulness
	dcs := data.CollectionStats{
		Folders:   ctrl.status.Folders,
		Objects:   ctrl.status.Metrics.Objects,
		Successes: ctrl.status.Metrics.Successes,
		Bytes:     ctrl.status.Metrics.Bytes,
		Details:   ctrl.status.String(),
	}

	ctrl.wg = &sync.WaitGroup{}
	ctrl.status = support.ControllerOperationStatus{}

	return &dcs
}

// UpdateStatus is used by initiated tasks to indicate completion
func (ctrl *Controller) UpdateStatus(status *support.ControllerOperationStatus) {
	defer ctrl.wg.Done()

	if status == nil {
		return
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	ctrl.status = support.MergeStatus(ctrl.status, *status)
}

// Status returns the current status of the controller process.
func (ctrl *Controller) Status() support.ControllerOperationStatus {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	return ctrl.status
}

// PrintableStatus returns a string formatted version of the status.
func (ctrl *Controller) PrintableStatus() string {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	return ctrl.status.String()
}

func (ctrl *Controller) incrementAwaitingMessages() {
	ctrl.wg.Add(1)
}

func (ctrl *Controller) CacheItemInfo(dii details.ItemInfo) {
	if dii.Groups != nil {
		ctrl.backupDriveIDNames.Add(dii.Groups.DriveID, dii.Groups.DriveName)
		ctrl.backupSiteIDWebURL.Add(dii.Groups.SiteID, dii.Groups.WebURL)
	}

	if dii.SharePoint != nil {
		ctrl.backupDriveIDNames.Add(dii.SharePoint.DriveID, dii.SharePoint.DriveName)
		ctrl.backupSiteIDWebURL.Add(dii.SharePoint.SiteID, dii.SharePoint.WebURL)
	}

	if dii.OneDrive != nil {
		ctrl.backupDriveIDNames.Add(dii.OneDrive.DriveID, dii.OneDrive.DriveName)
	}
}

// ---------------------------------------------------------------------------
// Resource Lookup Handling
// ---------------------------------------------------------------------------

var _ idname.GetResourceIDAndNamer = &resourceGetter{}

type resourceGetter struct {
	enum   resource.Category
	getter getIDAndNamer
}

type getIDAndNamer interface {
	GetIDAndName(
		ctx context.Context,
		owner string,
		cc api.CallConfig,
	) (
		ownerID string,
		ownerName string,
		err error,
	)
}

// GetResourceIDAndNameFrom looks up the resource's canonical id and display name.
// If the resource is present in the idNameSwapper, then that interface's id and
// name values are returned.  As a fallback, the resource calls the discovery
// api to fetch the user or site using the resource value. This fallback assumes
// that the resource is a well formed ID or display name of appropriate design
// (PrincipalName for users, WebURL for sites).
func (r resourceGetter) GetResourceIDAndNameFrom(
	ctx context.Context,
	owner string,
	ins idname.Cacher,
) (idname.Provider, error) {
	if ins != nil {
		if n, ok := ins.NameOf(owner); ok {
			return idname.NewProvider(owner, n), nil
		} else if i, ok := ins.IDOf(owner); ok {
			return idname.NewProvider(i, owner), nil
		}
	}

	ctx = clues.Add(ctx, "owner_identifier", owner)

	var (
		id, name string
		err      error
	)

	id, name, err = r.getter.GetIDAndName(ctx, owner, api.CallConfig{})
	if err != nil {
		return nil, clues.Stack(err)
	}

	if len(id) == 0 || len(name) == 0 {
		return nil, clues.Stack(core.ErrNotFound)
	}

	return idname.NewProvider(id, name), nil
}

// PopulateProtectedResourceIDAndName takes the provided owner identifier and produces
// the owner's name and ID from that value.  Returns an error if the owner is
// not recognized by the current tenant.
//
// The id-name cacher is optional.  Some processes will look up all owners in
// the tenant before reaching this step.  In that case, the data gets handed
// down for this func to consume instead of performing further queries.  The
// data gets stored inside the controller instance for later re-use.
func (ctrl *Controller) PopulateProtectedResourceIDAndName(
	ctx context.Context,
	resourceID string, // input value, can be either id or name
	ins idname.Cacher,
) (idname.Provider, error) {
	// Snapshot the handler under the lock rather than holding the lock
	// across the lookup call below: the lookup can make a real Graph
	// request, and holding mu for that long would serialize unrelated
	// concurrent operations (status updates, another EnsureResourceHandler
	// call) against it for no reason. The snapshot itself is what the race
	// was on - see mu's doc comment on the Controller struct.
	ctrl.mu.Lock()
	rh := ctrl.resourceHandler
	ctrl.mu.Unlock()

	if rh == nil {
		return nil, clues.StackWC(ctx, ErrNoResourceLookup)
	}

	pr, err := rh.GetResourceIDAndNameFrom(ctx, resourceID, ins)
	if err != nil {
		return nil, clues.Wrap(err, "identifying resource owner")
	}

	ctrl.mu.Lock()
	ctrl.IDNameLookup = idname.NewCache(map[string]string{pr.ID(): pr.Name()})
	ctrl.mu.Unlock()

	return pr, nil
}
