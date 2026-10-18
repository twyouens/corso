// This file holds the compiled examples for the six capability areas
// docs/library-api.md documents: opening a repository, backing up,
// browsing a point's folder tree and items, finding items by name,
// listing and deleting points by tag, and running maintenance.
//
// They are godoc examples with no "Output:" comment, so `go test` compiles
// them and never runs them - which is the point. Nothing here needs a
// repository, a Microsoft 365 tenant or a credential, and every snippet
// docs/library-api.md shows is a copy of one of these functions, so the
// compiler (and `go vet`'s own check that an ExampleType_Method name
// matches a real exported method) is what keeps that documentation true
// rather than someone's memory of it.
//
// Deliberately in package engine_test, not package engine: an example
// that could reach an unexported helper would not be the same shape as
// the code k3ep writes.
package engine_test

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/engine"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/storage"
)

// The values below stand in for the ones k3ep derives server-side, for
// one tenant, before any engine call. They are function calls rather than
// string literals on purpose: a repository key or client secret in an
// example is the thing that gets copied into real code (chunk 0.6's
// review found exactly that in src/cmd/backupexample), and a storage
// location or credential that a tenant could influence is the whole of
// the trust-model warning in RepositoryConfig's own doc comment.
//
// Each returns an empty string here because these examples are compiled,
// never run.
func repositoryKeyFromKeyManager() string { return "" } // ENC-1: unwrapped for this one call
func storagePathForTenant() string        { return "" } // derived from the tenant record, never from a request
func m365AppCredentials() (string, string, string) { // tenant ID, client ID, client secret
	return "", "", ""
}

// tenantTags are the tags k3ep puts on every backup point and uses to
// scope every later list, delete or search. The keys are namespaced:
// pkg/engine rejects the bare key "tenant" and any key starting with
// "sc-", because the fork's own garbage collector reads those to decide
// which assist bases survive. The values are k3ep's own IDs, generated
// server-side - never a string a tenant supplied.
func tenantTags() map[string]string {
	return map[string]string{
		"k3ep-tenant": "tenant-uuid",
		"k3ep-policy": "policy-uuid",
		"k3ep-object": "object-uuid",
	}
}

// exampleConfig builds the RepositoryConfig the examples below reuse.
// ExampleInitializeRepository shows the same thing in full, including the
// S3 variant.
func exampleConfig() engine.RepositoryConfig {
	tenantID, clientID, clientSecret := m365AppCredentials()

	acct, _ := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365:          credentials.M365{AzureClientID: clientID, AzureClientSecret: clientSecret},
			AzureTenantID: tenantID,
		})

	st, _ := storage.NewStorage(
		storage.ProviderFilesystem,
		&storage.FilesystemConfig{Path: storagePathForTenant()},
		storage.CommonConfig{
			Corso:       credentials.Corso{CorsoPassphrase: repositoryKeyFromKeyManager()},
			KopiaCfgDir: storagePathForTenant() + "/kopia-config",
		})

	return engine.RepositoryConfig{
		Storage: st,
		Account: acct,
		Options: control.DefaultOptions(),
	}
}

// ExampleInitializeRepository creates a tenant's repository, with every
// setting - storage location, encryption key, Microsoft 365 credentials -
// supplied by the caller. pkg/engine reads no environment variable,
// configuration file or flag, so there is no ambient value to fall back
// on and nothing to misconfigure by omission.
//
// One repository holds exactly one tenant: tags are a label, not an
// authorisation boundary, so tenant separation is this choice of storage
// location and key, not the tags on the points inside it.
func ExampleInitializeRepository() {
	ctx := context.Background()

	tenantID, clientID, clientSecret := m365AppCredentials()

	acct, err := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365:          credentials.M365{AzureClientID: clientID, AzureClientSecret: clientSecret},
			AzureTenantID: tenantID,
		})
	if err != nil {
		fmt.Println("building the M365 account:", err)
		return
	}

	// A mounted filesystem path. For S3-compatible storage, swap in:
	//
	//	storage.ProviderS3,
	//	&storage.S3Config{
	//		AWS:      credentials.AWS{AccessKey: ak, SecretKey: sk},
	//		Bucket:   bucket,
	//		Endpoint: endpoint,
	//		Prefix:   prefix,
	//	},
	//
	// AccessKey and SecretKey are required and are never inferred: there
	// is no instance-role, shared-credentials-file or SDK-fallback path
	// in this fork, and a blank or whitespace-only key is rejected before
	// any network call with engine.ErrMissingStorageCredentials.
	st, err := storage.NewStorage(
		storage.ProviderFilesystem,
		&storage.FilesystemConfig{Path: storagePathForTenant()},
		storage.CommonConfig{
			// The repository's own encryption key. Lose it and the
			// repository is unreadable; log it and the whole repository is
			// compromised. Never format a Storage, an Account or a
			// RepositoryConfig with anything but their own String/LogValue/
			// MarshalJSON methods - and never with YAML or encoding/gob,
			// which walk the credential maps those methods protect.
			Corso:       credentials.Corso{CorsoPassphrase: repositoryKeyFromKeyManager()},
			KopiaCfgDir: storagePathForTenant() + "/kopia-config",
		})
	if err != nil {
		fmt.Println("building the storage configuration:", err)
		return
	}

	cfg := engine.RepositoryConfig{
		Storage: st,
		Account: acct,
		Options: control.DefaultOptions(),
	}

	// Opening a repository also connects and verifies the Microsoft 365
	// account, and installs a process-global Graph concurrency clamp that
	// the first open in this process latches for every later one - see
	// docs/library-api.md.
	repo, err := engine.InitializeRepository(ctx, cfg)
	if err != nil {
		fmt.Println("initialising the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	fmt.Println("repository ID:", repo.ID())
}

// ExampleOpenRepository connects to a repository that already exists, at
// the same storage location and with the same key it was created with.
func ExampleOpenRepository() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	fmt.Println("repository ID:", repo.ID())
}

// ExampleRepository_Backup runs one backup with caller-supplied tags and
// a progress callback.
func ExampleRepository_Backup() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	sel := selectors.NewExchangeBackup([]string{"user@example.test"})
	sel.Include(sel.MailFolders(selectors.Any(), selectors.PrefixMatch()))
	sel.DiscreteOwner = "user@example.test"

	point, err := repo.Backup(ctx, engine.BackupRequest{
		Selector: sel.Selector,
		Tags:     tenantTags(),
		// Called periodically on a goroutine Backup owns and joins before
		// returning, and exactly once more with Done true. Counts are real
		// server-confirmed counters and carry no item name or content.
		// Nothing in pkg/engine writes to stdout; this callback is the only
		// progress channel there is.
		Progress: func(p engine.ProgressSnapshot) {
			fmt.Println("progress:", p.Counts, "done:", p.Done)
		},
	})
	if err != nil {
		// A backup that returns an error may still have persisted an
		// assist point carrying these tags, holding real content that
		// occupies storage. It is listable and deletable through the tag
		// filter below; it is not restorable.
		fmt.Println("running the backup:", err)
		return
	}

	fmt.Println(point.ID, point.Type, point.ItemsRead, point.BytesUploaded, point.ErrorCount)
}

// ExampleRepository_ListBackupPoints lists one tenant's backup points.
//
// The tag filter is not optional in practice: ListBackupPoints(ctx, nil)
// enumerates every point in the repository, so a tenant-scoped filter,
// built from values k3ep generated server-side, is what makes the result
// one tenant's. The listing includes assist and preview points, which are
// real and occupy storage but are not restorable on their own - Type and
// IsIndependentlyRestorable are how a caller tells them apart.
func ExampleRepository_ListBackupPoints() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	points, err := repo.ListBackupPoints(ctx, map[string]string{
		"k3ep-tenant": tenantTags()["k3ep-tenant"],
		"k3ep-object": tenantTags()["k3ep-object"],
	})
	if err != nil {
		fmt.Println("listing backup points:", err)
		return
	}

	for _, p := range points {
		if !p.Type.IsIndependentlyRestorable() {
			// Show it (it is the tenant's data, and deletable), but never
			// offer it as a point in time to restore from.
			fmt.Println(p.ID, "incomplete:", p.Type)
			continue
		}

		fmt.Println(p.ID, p.CreatedAt.Format(time.RFC3339), p.ItemsRead)
	}
}

// ExampleRepository_DeleteBackupPoints deletes the points matching a tag
// filter and returns the IDs it actually deleted, which is what k3ep
// records in its audit event (AUD-1).
//
// The filter must be non-empty (engine.ErrEmptyTagFilter): there is no
// "delete everything" call. Matching is conjunctive and byte-exact, so
// include the tenant tag in every delete, not just the object tag.
func ExampleRepository_DeleteBackupPoints() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	deleted, err := repo.DeleteBackupPoints(ctx, map[string]string{
		"k3ep-tenant": tenantTags()["k3ep-tenant"],
		"k3ep-object": tenantTags()["k3ep-object"],
	})
	if err != nil {
		fmt.Println("deleting backup points:", err)
		return
	}

	fmt.Println("deleted", len(deleted), "backup point(s):", deleted)
}

// ExampleRepository_ListFolders browses one backup point's folder tree.
// It reads the point's own details manifest only: no Graph call, no item
// content fetched or decrypted, so a mailbox that no longer exists in
// Microsoft 365 is still browsable.
func ExampleRepository_ListFolders() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	// A point ID is not self-authorising. Get it from a tenant-scoped
	// ListBackupPoints call (or check it against k3ep's own records)
	// before browsing it: ListFolders and ListItems take a bare ID and
	// check no tags at all.
	points, err := repo.ListBackupPoints(ctx, tenantTags())
	if err != nil || len(points) == 0 {
		fmt.Println("listing backup points:", err)
		return
	}

	tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{
		BackupPointID: points[0].ID,
		// Left false, an assist or preview point is refused with
		// engine.ErrIncompleteBackupPoint rather than returning a fragment
		// of a tree that looks complete. Set it only where a partial tree
		// is the point, such as enumerating an assist point for erasure,
		// and then check tree.IndependentlyRestorable before presenting it.
		AllowIncompletePoint: false,
	})
	if err != nil {
		fmt.Println("listing folders:", err)
		return
	}

	// Folders is flat and deterministically ordered. Build the hierarchy
	// from ParentID ("" is top level), not from Path: Path is a display
	// string joined with "/" and folder names may contain "/".
	children := map[string][]engine.Folder{}
	for _, f := range tree.Folders {
		children[f.ParentID] = append(children[f.ParentID], f)
	}

	for _, f := range children[""] {
		// Service and Category are load-bearing, not decoration: a mail
		// folder, a calendar and a contacts folder all arrive as top-level
		// Exchange folders with nothing but a display name to tell them
		// apart, and two of them may share that name. A SharePoint list's
		// container is named by the list's Microsoft 365 ID, not its
		// title, so label a Category "lists" folder from the single item
		// inside it instead.
		fmt.Println(f.Name, f.Service, f.Category, f.DriveName, len(children[f.ID]))
	}
}

// ExampleRepository_ListItems lists the items in one folder of one backup
// point, each with the stable ID a later restore or erasure is handed.
//
// The durable handle is the pair (backup point ID, item ID). An item ID is
// only meaningful inside the point it was listed from: for OneDrive and
// SharePoint the ID is derived from the folder chain's display names, so
// renaming or moving an ancestor folder changes every item ID beneath it
// in the next point; for Exchange the chain is container IDs, so a rename
// is safe but a move is not.
func ExampleRepository_ListItems() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	points, err := repo.ListBackupPoints(ctx, tenantTags())
	if err != nil || len(points) == 0 {
		fmt.Println("listing backup points:", err)
		return
	}

	pointID := points[0].ID

	list, err := repo.ListItems(ctx, engine.ItemsRequest{
		BackupPointID: pointID,
		// Empty FolderID returns every item in the point; a FolderID from
		// another point is engine.ErrFolderNotFound, never silently empty.
		FolderID: "",
	})
	if err != nil {
		fmt.Println("listing items:", err)
		return
	}

	for _, it := range list.Items {
		// Store the pair, not it.ID alone.
		handle := struct{ BackupPointID, ItemID string }{list.BackupPointID, it.ID}

		// A SharePoint list is one item: its rows are not individually
		// addressable, so ItemCount is all browse can say about what is
		// inside it, and restore and erasure are list-granular in v1.
		fmt.Println(handle, it.Name, it.SizeBytes, it.ItemCount, it.Service, it.Category)
	}
}

// ExampleRepository_FindItemsByName finds items by name across one
// object's backup points. It matches names only - never message bodies,
// file contents or attachments - against what the points' details
// manifests already recorded, so it decrypts nothing.
//
// The result is a struct rather than a slice for one reason, and it is the
// reason this example exists: a search that could not read every matching
// point must say so. Ignore UnreadableBackupPointIDs and an incomplete
// search renders as a confident "nothing found", which is exactly the
// wrong answer when the question is whether an erasure actually took
// effect.
func ExampleRepository_FindItemsByName() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	res, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
		// Required and non-empty (engine.ErrEmptyTagFilter), and scoped to
		// one tenant and one object: the search reads one details manifest
		// per matching point, so its cost is the number of points the tags
		// select.
		Tags:  tenantTags(),
		Name:  "quarterly report",
		Match: engine.NameMatchContains,
	})
	if err != nil {
		fmt.Println("finding items:", err)
		return
	}

	if len(res.UnreadableBackupPointIDs) > 0 {
		// Surface this to the caller and to the audit record. The result
		// is PARTIAL: these points matched the tags and were not searched.
		sort.Strings(res.UnreadableBackupPointIDs)
		fmt.Println("search incomplete; unsearched points:", res.UnreadableBackupPointIDs)
	}

	for _, f := range res.Items {
		fmt.Println(
			f.BackupPointID,
			f.Item.ID,
			f.Item.Name,
			f.Item.FolderPath,
			f.BackupPointType,
			f.BackupPointIndependentlyRestorable,
			f.BackupPointCreatedAt.Format(time.RFC3339))
	}
}

// ExampleRepository_RunMaintenance runs Kopia maintenance: garbage
// collection of unreferenced blobs and index compaction. It blocks until
// it finishes, and the repository stays readable throughout - maintenance
// never drops a blob a current backup point still references.
func ExampleRepository_RunMaintenance() {
	ctx := context.Background()

	repo, err := engine.OpenRepository(ctx, exampleConfig())
	if err != nil {
		fmt.Println("opening the repository:", err)
		return
	}

	defer func() { _ = repo.Close(ctx) }()

	// Metadata-only passes are cheap and can run often; a full pass
	// (Metadata false) still has to run regularly for blobs to be
	// reclaimed. Force skips the fork's "not too often" safety check and
	// is for tests and deliberate off-schedule runs.
	if err := repo.RunMaintenance(ctx, engine.MaintenanceOptions{
		Metadata: true,
		Force:    false,
	}); err != nil {
		fmt.Println("running maintenance:", err)
		return
	}

	fmt.Println("maintenance finished")
}
