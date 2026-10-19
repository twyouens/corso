package mock

import (
	"context"
	"sync"

	"github.com/microsoft/kiota-abstractions-go/serialization"

	"github.com/twyouens/corso/src/pkg/backup/details"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/fault"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
)

// ---------------------------------------------------------------------------
// get and serialize item mock
// ---------------------------------------------------------------------------

// ItemGetSerialize is a mock item getter and serializer. Collections call
// GetItem and Serialize from one goroutine per item, so both methods update
// their counters under mu. Read GetCount and SerializeCount only once the
// collection under test has finished streaming.
type ItemGetSerialize struct {
	GetData        serialization.Parsable
	GetCount       int
	GetErr         error
	SerializeCount int
	SerializeErr   error

	mu sync.Mutex
}

func (m *ItemGetSerialize) GetItem(
	context.Context,
	string, string,
	*fault.Bus,
) (serialization.Parsable, *details.ExchangeInfo, error) {
	m.mu.Lock()
	m.GetCount++
	m.mu.Unlock()

	return m.GetData, &details.ExchangeInfo{}, m.GetErr
}

func (m *ItemGetSerialize) Serialize(
	ctx context.Context,
	p serialization.Parsable,
	_ string, _ string,
) ([]byte, error) {
	m.mu.Lock()
	m.SerializeCount++
	m.mu.Unlock()

	if p == nil || m.SerializeErr != nil {
		return nil, m.SerializeErr
	}

	return api.Mail{}.Serialize(ctx, p, "", "")
}

func DefaultItemGetSerialize() *ItemGetSerialize {
	return &ItemGetSerialize{}
}

// ---------------------------------------------------------------------------
// can skip item failure mock
// ---------------------------------------------------------------------------

type canSkipFailChecker struct {
	canSkip bool
}

func (m canSkipFailChecker) CanSkipItemFailure(
	err error,
	resourceID string,
	opts control.Options,
) (fault.SkipCause, bool) {
	return fault.SkipCause("testing"), m.canSkip
}

func NeverCanSkipFailChecker() *canSkipFailChecker {
	return &canSkipFailChecker{}
}
