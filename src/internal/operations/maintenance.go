package operations

import (
	"context"
	"time"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/common/crash"
	"github.com/twyouens/corso/src/internal/kopia"
	"github.com/twyouens/corso/src/internal/stats"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/control/repository"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/store"
)

// MaintenanceOperation wraps an operation with restore-specific props.
type MaintenanceOperation struct {
	operation
	Results MaintenanceResults
	mOpts   repository.Maintenance
}

// MaintenanceResults aggregate the details of the results of the operation.
type MaintenanceResults struct {
	stats.StartAndEndTime
}

// NewMaintenanceOperation constructs and validates a maintenance operation.
func NewMaintenanceOperation(
	ctx context.Context,
	opts control.Options,
	kw *kopia.Wrapper,
	storer store.BackupStorer,
	mOpts repository.Maintenance,
) (MaintenanceOperation, error) {
	op := MaintenanceOperation{
		operation: newOperation(opts, count.New(), kw, storer),
		mOpts:     mOpts,
	}

	err := op.validate()

	return op, clues.Stack(err).OrNil()
}

func (op *MaintenanceOperation) Run(ctx context.Context) (err error) {
	defer func() {
		if crErr := crash.Recovery(ctx, recover(), "maintenance"); crErr != nil {
			err = crErr
		}
	}()

	op.Results.StartedAt = time.Now()

	return op.do(ctx)
}

func (op *MaintenanceOperation) do(ctx context.Context) error {
	defer func() {
		op.Results.CompletedAt = time.Now()
	}()

	err := op.operation.kopia.RepoMaintenance(ctx, op.store, op.mOpts, op.Errors)
	if err != nil {
		op.Status = Failed
		return clues.Wrap(err, "running maintenance operation")
	}

	op.Status = Completed

	return nil
}
