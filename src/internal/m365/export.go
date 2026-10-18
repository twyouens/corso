package m365

import (
	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/internal/m365/service/exchange"
	"github.com/twyouens/corso/src/internal/m365/service/groups"
	"github.com/twyouens/corso/src/internal/m365/service/onedrive"
	"github.com/twyouens/corso/src/internal/m365/service/sharepoint"
	"github.com/twyouens/corso/src/internal/operations/inject"
	"github.com/twyouens/corso/src/pkg/path"
)

// NewServiceHandler returns an instance of a struct capable of running various
// operations for a given service.
func (ctrl *Controller) NewServiceHandler(
	service path.ServiceType,
) (inject.ServiceHandler, error) {
	// Use setResourceHandler's own return value rather than a second,
	// separate read of ctrl.resourceHandler: that field is shared across
	// concurrently reused Controllers (see its doc comment), and a second
	// unguarded read here could observe a different, later write than the
	// one this call itself just made.
	rh := ctrl.setResourceHandler(service)

	switch service {
	case path.OneDriveService:
		return onedrive.NewOneDriveHandler(ctrl.AC, rh), nil

	case path.SharePointService:
		return sharepoint.NewSharePointHandler(ctrl.AC, rh), nil

	case path.GroupsService:
		return groups.NewGroupsHandler(ctrl.AC, rh), nil

	case path.ExchangeService:
		return exchange.NewExchangeHandler(ctrl.AC, rh), nil
	}

	return nil, clues.New("unrecognized service").
		With("service_type", service.String())
}
