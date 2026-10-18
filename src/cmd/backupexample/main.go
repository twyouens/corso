// Command backupexample is chunk 0.6's proof of REPO-2/REPO-3: it backs up
// a seeded Exchange mailbox and lists the resulting backup point using
// nothing but src/pkg/engine (and the handful of other src/pkg packages
// needed to build its inputs - pkg/account, pkg/storage, pkg/selectors,
// pkg/control, pkg/credentials). It imports no src/internal package - see
// TestExample_ImportsNoInternalPackage in this directory, which parses this
// file's own import block and fails the build if that ever stops being
// true. That test is the point of this program; everything else here is
// just enough plumbing to make it a real, runnable backup rather than a
// stub.
//
// Like every program under src/cmd, this is where reading the process
// environment belongs - src/pkg/engine itself never does. Required:
//
//	AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET - a Microsoft 365
//	  application's credentials (the same three chunk 0.3's seeder and test
//	  tenant harness use).
//	SEED_USER_ID - the licensed M365 user chunk 0.3 seeded; this program
//	  backs up that user's Exchange Inbox.
//
// Optional:
//
//	BACKUPEXAMPLE_REPO_PATH - a filesystem directory for the Kopia
//	  repository. Defaults to a fresh temporary directory.
//
// Required, with no default - review flagged a hardcoded fallback
// passphrase in an earlier version of this file as exactly the kind of
// thing that gets copied into real code, and example code is what gets
// copied:
//
//	BACKUPEXAMPLE_PASSPHRASE - the repository's encryption key. No
//	  default: this program fails closed rather than silently backing a
//	  real mailbox up under a passphrase anyone reading this file also
//	  knows.
//
// Run from src/ with:
//
//	GO111MODULE=on GOWORK=off go run ./cmd/backupexample
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/engine"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "backupexample:", err)
		os.Exit(1)
	}
}

func run() error {
	tenantID := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	clientSecret := os.Getenv("AZURE_CLIENT_SECRET")
	userID := os.Getenv("SEED_USER_ID")

	passphrase := os.Getenv("BACKUPEXAMPLE_PASSPHRASE")

	for name, v := range map[string]string{
		"AZURE_TENANT_ID":          tenantID,
		"AZURE_CLIENT_ID":          clientID,
		"AZURE_CLIENT_SECRET":      clientSecret,
		"SEED_USER_ID":             userID,
		"BACKUPEXAMPLE_PASSPHRASE": passphrase,
	} {
		if len(v) == 0 {
			return fmt.Errorf("missing required environment variable %s", name)
		}
	}

	repoPath := os.Getenv("BACKUPEXAMPLE_REPO_PATH")
	if len(repoPath) == 0 {
		tmp, err := os.MkdirTemp("", "backupexample-repo-")
		if err != nil {
			return fmt.Errorf("creating a temporary repository directory: %w", err)
		}

		repoPath = tmp
	}

	// Kopia's own local config/cache directory. Left unset, it defaults to
	// a predictable "/tmp/repository-<hash>.config" (internal/kopia/conn.go),
	// a filename anyone on the same machine could guess or watch for; set
	// it explicitly, alongside repoPath, so the only predictable thing
	// about this example's on-disk footprint is its own temp directory.
	kopiaCfgDir := filepath.Join(repoPath, "kopia-config")

	ctx := context.Background()

	acct, err := account.NewAccount(
		account.ProviderM365,
		account.M365Config{
			M365:          credentials.M365{AzureClientID: clientID, AzureClientSecret: clientSecret},
			AzureTenantID: tenantID,
		})
	if err != nil {
		return fmt.Errorf("building the M365 account: %w", err)
	}

	st, err := storage.NewStorage(
		storage.ProviderFilesystem,
		&storage.FilesystemConfig{Path: repoPath},
		storage.CommonConfig{
			Corso:       credentials.Corso{CorsoPassphrase: passphrase},
			KopiaCfgDir: kopiaCfgDir,
		})
	if err != nil {
		return fmt.Errorf("building the storage configuration: %w", err)
	}

	cfg := engine.RepositoryConfig{
		Storage: st,
		Account: acct,
		Options: control.DefaultOptions(),
	}

	repo, err := engine.InitializeRepository(ctx, cfg)
	if err != nil {
		// A repository already exists at repoPath (e.g. this program ran
		// here before): connect to it instead.
		repo, err = engine.OpenRepository(ctx, cfg)
		if err != nil {
			return fmt.Errorf("opening the repository: %w", err)
		}
	}

	defer func() {
		if err := repo.Close(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "backupexample: closing repository:", err)
		}
	}()

	fmt.Println("repository ID:", repo.ID())

	sel := selectors.NewExchangeBackup([]string{userID})
	sel.Include(sel.MailFolders([]string{"Inbox"}, selectors.PrefixMatch()))
	sel.DiscreteOwner = userID

	tags := map[string]string{
		"k3ep-tenant": tenantID,
		"k3ep-policy": "backupexample-demo-policy",
		"k3ep-object": userID,
	}

	point, err := repo.Backup(ctx, engine.BackupRequest{
		Selector: sel.Selector,
		Tags:     tags,
		Progress: func(p engine.ProgressSnapshot) {
			if p.Done {
				fmt.Println("backup finished:", p.Counts)
				return
			}

			fmt.Println("backup progress:", p.Counts)
		},
	})
	if err != nil {
		return fmt.Errorf("running the backup: %w", err)
	}

	fmt.Printf(
		"backup point %s (type %s) created %s: %d items read, %d bytes uploaded, tags %v\n",
		point.ID,
		point.Type,
		point.CreatedAt.Format(time.RFC3339),
		point.ItemsRead,
		point.BytesUploaded,
		point.Tags)

	points, err := repo.ListBackupPoints(ctx, map[string]string{"k3ep-object": userID})
	if err != nil {
		return fmt.Errorf("listing backup points: %w", err)
	}

	fmt.Printf("found %d backup point(s) for object %s:\n", len(points), userID)

	for _, p := range points {
		fmt.Printf("  %s (type %s, created %s, tags %v)\n", p.ID, p.Type, p.CreatedAt.Format(time.RFC3339), p.Tags)
	}

	return browse(ctx, repo, point, userID)
}

// browse is chunk 0.7's half of this example: it walks the backup point
// just created, lists one folder's items, and finds an item by name -
// again using nothing but pkg/engine, so this directory's
// TestExample_ImportsNoInternalPackage covers the browse API too and not
// just 0.6's.
//
// Worth copying from, if you are writing the k3ep side: note that it
// checks IsIndependentlyRestorable before presenting a tree, rather than
// assuming every point ListBackupPoints returned is a complete one.
func browse(
	ctx context.Context,
	repo *engine.Repository,
	point engine.BackupPoint,
	userID string,
) error {
	if !point.Type.IsIndependentlyRestorable() {
		// Not an error: an assist point is real, tagged content a tenant
		// must be able to find and delete. It is just not a complete
		// point in time, so it is not something to browse for restore.
		// engine.ListFolders would return ErrIncompleteBackupPoint here
		// unless asked otherwise.
		fmt.Printf(
			"backup point %s is of type %s, so it is not independently restorable; not browsing it\n",
			point.ID,
			point.Type)

		return nil
	}

	tree, err := repo.ListFolders(ctx, engine.FolderTreeRequest{BackupPointID: point.ID})
	if err != nil {
		return fmt.Errorf("listing the backup point's folders: %w", err)
	}

	fmt.Printf("backup point %s holds %d folder(s):\n", tree.BackupPointID, len(tree.Folders))

	for _, f := range tree.Folders {
		fmt.Printf(
			"  %s [%s/%s] id=%s parent=%q %d bytes\n",
			f.Path, f.Service, f.Category, f.ID, f.ParentID, f.SizeBytes)
	}

	if len(tree.Folders) == 0 {
		fmt.Println("no folders to list items from")
		return nil
	}

	// Items directly inside the first folder, each with the stable ID a
	// later restore or erasure would be handed.
	folder := tree.Folders[0]

	list, err := repo.ListItems(ctx, engine.ItemsRequest{
		BackupPointID: point.ID,
		FolderID:      folder.ID,
	})
	if err != nil {
		return fmt.Errorf("listing items in a folder: %w", err)
	}

	fmt.Printf("folder %s holds %d item(s) directly:\n", folder.Path, len(list.Items))

	for _, it := range list.Items {
		fmt.Printf(
			"  %s id=%s %d bytes created=%s modified=%s\n",
			it.Name,
			it.ID,
			it.SizeBytes,
			it.CreatedAt.Format(time.RFC3339),
			it.ModifiedAt.Format(time.RFC3339))
	}

	if len(list.Items) == 0 {
		return nil
	}

	// Find that first item again by (part of) its name, across every
	// backup point this object has - RES-2. Name matching only: nothing
	// here reads item content.
	query := list.Items[0].Name

	found, err := repo.FindItemsByName(ctx, engine.FindItemsRequest{
		Tags: map[string]string{"k3ep-object": userID},
		Name: query,
	})
	if err != nil {
		return fmt.Errorf("finding items by name: %w", err)
	}

	fmt.Printf("found %d item(s) matching %q across this object's backup points:\n", len(found), query)

	for _, f := range found {
		fmt.Printf(
			"  %s id=%s in folder %q of backup point %s (type %s, created %s)\n",
			f.Item.Name,
			f.Item.ID,
			f.Item.FolderPath,
			f.BackupPointID,
			f.BackupPointType,
			f.BackupPointCreatedAt.Format(time.RFC3339))
	}

	return nil
}
