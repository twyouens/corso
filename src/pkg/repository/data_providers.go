package repository

import (
	"context"
	"fmt"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/data"
	"github.com/twyouens/corso/src/internal/m365"
	"github.com/twyouens/corso/src/internal/observe"
	"github.com/twyouens/corso/src/internal/operations/inject"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/path"
	"github.com/twyouens/corso/src/pkg/store"
)

type DataProvider interface {
	inject.BackupProducer
	// Required for backups right now.
	inject.PopulateProtectedResourceIDAndNamer

	inject.ToServiceHandler

	VerifyAccess(ctx context.Context) error
	DeserializeMetadataFiles(
		ctx context.Context,
		colls []data.RestoreCollection,
	) ([]store.MetadataFile, error)
}

type DataProviderConnector interface {
	// ConnectDataProvider initializes configurations
	// and establishes the client connection with the
	// data provider for this operation.
	ConnectDataProvider(
		ctx context.Context,
		pst path.ServiceType,
	) error
	// DataProvider retrieves the data provider.
	DataProvider() DataProvider
}

func (r *repository) DataProvider() DataProvider {
	return r.Provider
}

func (r *repository) ConnectDataProvider(
	ctx context.Context,
	pst path.ServiceType,
) error {
	var (
		provider DataProvider
		err      error
	)

	switch r.Account.Provider {
	case account.ProviderM365:
		provider, err = connectToM365(ctx, *r, pst)
	default:
		err = clues.NewWC(ctx, "unrecognized provider")
	}

	if err != nil {
		return clues.Wrap(err, "connecting data provider")
	}

	if err := provider.VerifyAccess(ctx); err != nil {
		return clues.Wrap(err, fmt.Sprintf("verifying %s account connection", r.Account.Provider))
	}

	r.Provider = provider

	return nil
}

func connectToM365(
	ctx context.Context,
	r repository,
	pst path.ServiceType,
) (*m365.Controller, error) {
	if r.Provider != nil {
		ctrl, ok := r.Provider.(*m365.Controller)
		if !ok {
			// if the provider is initialized to a non-m365 controller, we should not
			// attempt to connnect to m365 afterward.
			return nil, clues.New("Attempted to connect to multiple data providers")
		}

		// A reused controller may have been created for a different (or, for
		// a repository opened before any backup chose a service, an
		// unknown) service. Re-derive its resource handler for pst every
		// time, not just on first creation, so a repository instance used
		// for more than one service in its lifetime - which pkg/engine
		// deliberately allows, by connecting a provider before any Backup
		// call is made - never carries over a stale or nil handler. See
		// Controller.EnsureResourceHandler's doc comment.
		ctrl.EnsureResourceHandler(pst)

		return ctrl, nil
	}

	progressMessage := observe.MessageWithCompletion(ctx, observe.DefaultCfg(), "Connecting to M365")
	defer close(progressMessage)

	ctrl, err := m365.NewController(
		ctx,
		r.Account,
		pst,
		r.Opts,
		r.counter)
	if err != nil {
		return nil, clues.Wrap(err, "creating m365 client controller")
	}

	return ctrl, nil
}
