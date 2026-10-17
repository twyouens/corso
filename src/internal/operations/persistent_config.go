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
)

// PersistentConfig wraps an operation that deals with repo configuration.
type PersistentConfigOperation struct {
	operation
	Results    PersistentConfigResults
	configOpts repository.PersistentConfig
}

// PersistentConfigResults aggregate the details of the results of the operation.
type PersistentConfigResults struct {
	stats.StartAndEndTime
}

// NewPersistentConfigOperation constructs and validates an operation to change
// various persistent config parameters like the minimum epoch duration for the
// kopia index.
func NewPersistentConfigOperation(
	ctx context.Context,
	opts control.Options,
	kw *kopia.Wrapper,
	configOpts repository.PersistentConfig,
) (PersistentConfigOperation, error) {
	op := PersistentConfigOperation{
		operation:  newOperation(opts, count.New(), kw, nil),
		configOpts: configOpts,
	}

	// Don't run validation because we don't populate the model store.

	return op, nil
}

func (op *PersistentConfigOperation) Run(ctx context.Context) (err error) {
	defer func() {
		if crErr := crash.Recovery(ctx, recover(), "persistent_config"); crErr != nil {
			err = crErr
		}
	}()

	return op.do(ctx)
}

func (op *PersistentConfigOperation) do(ctx context.Context) error {
	op.Results.StartedAt = time.Now()

	defer func() {
		op.Results.CompletedAt = time.Now()
	}()

	err := op.operation.kopia.UpdatePersistentConfig(ctx, op.configOpts)
	if err != nil {
		op.Status = Failed
		return clues.Wrap(err, "running update persistent config operation")
	}

	op.Status = Completed

	return nil
}
