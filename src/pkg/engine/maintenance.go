package engine

import (
	"context"
	"fmt"

	ctrlRepo "github.com/twyouens/corso/src/pkg/control/repository"
)

// MaintenanceOptions configures one maintenance run.
type MaintenanceOptions struct {
	// Metadata, if true, runs a cheaper, metadata-only maintenance pass
	// instead of the full, complete pass. See Kopia's own documentation
	// for the difference; a full pass should be run regularly even if
	// metadata-only passes run more often in between.
	Metadata bool
	// Force skips the fork's own "don't run full maintenance too often"
	// safety check. Only needed for tests or deliberate off-schedule
	// runs.
	Force bool
}

// RunMaintenance runs Kopia repository maintenance (garbage collection of
// unreferenced blobs, index compaction) and blocks until it finishes. The
// repository remains fully readable throughout and afterwards: maintenance
// never deletes a blob a current backup point still references.
func (r *Repository) RunMaintenance(ctx context.Context, opts MaintenanceOptions) error {
	maintType := ctrlRepo.CompleteMaintenance
	if opts.Metadata {
		maintType = ctrlRepo.MetadataMaintenance
	}

	m, err := r.repo.NewMaintenance(ctx, ctrlRepo.Maintenance{
		Type:   maintType,
		Safety: ctrlRepo.FullMaintenanceSafety,
		Force:  opts.Force,
	})
	if err != nil {
		return fmt.Errorf("engine: preparing maintenance: %w", err)
	}

	if err := m.Run(ctx); err != nil {
		return fmt.Errorf("engine: running maintenance: %w", err)
	}

	return nil
}
