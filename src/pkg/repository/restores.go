package repository

import (
	"context"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/internal/operations"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/selectors"
	"github.com/twyouens/corso/src/pkg/store"
)

type Restorer interface {
	NewRestore(
		ctx context.Context,
		backupID string,
		sel selectors.Selector,
		restoreCfg control.RestoreConfig,
	) (operations.RestoreOperation, error)
}

// NewRestore generates a restoreOperation runner.
func (r repository) NewRestore(
	ctx context.Context,
	backupID string,
	sel selectors.Selector,
	restoreCfg control.RestoreConfig,
) (operations.RestoreOperation, error) {
	handler, err := r.Provider.NewServiceHandler(sel.PathService())
	if err != nil {
		return operations.RestoreOperation{}, clues.Stack(err)
	}

	return operations.NewRestoreOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		handler,
		r.Account,
		model.StableID(backupID),
		sel,
		restoreCfg,
		count.New())
}
