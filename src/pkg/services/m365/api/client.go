package api

import (
	"context"
	"io"
	"net/http"

	"github.com/alcionai/clues"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// ---------------------------------------------------------------------------
// interfaces
// ---------------------------------------------------------------------------

// Client is used to fulfill queries that are traditionally
// backed by GraphAPI. A struct is used in this case, instead
// of deferring to pure function wrappers, so that the boundary
// separates the granular implementation of the graphAPI and
// kiota away from the other packages.
type Client struct {
	Credentials account.M365Config

	// The Stable service is re-usable for any request.
	// This allows us to maintain performance across async requests.
	Stable graph.Servicer

	// The LargeItem graph servicer is configured specifically for
	// downloading large items such as drive item content or outlook
	// mail and event attachments.
	LargeItem graph.Servicer

	// The Requester provides a client specifically for calling
	// arbitrary urls instead of constructing queries using the
	// graph api client.
	Requester graph.Requester

	counter *count.Bus

	options control.Options
}

// NewClient produces a new exchange api client.  Must be used in
// place of creating an ad-hoc client struct.
func NewClient(
	creds account.M365Config,
	co control.Options,
	counter *count.Bus,
	opts ...graph.Option,
) (Client, error) {
	// The Graph concurrency limiter's capacity is a property of the clients
	// built here, taken from the parallelism this caller asked for in its own
	// control.Options.  It is prepended rather than appended so an explicit
	// graph.ConcurrencyLimit in opts still wins: options are applied in order.
	//
	// Nothing process-global is involved.  Two api.Clients built in one
	// process - two repositories, two tenants, two concurrent backups - each
	// get their own semaphore at their own capacity, and a client that fails
	// to build sets nothing for the clients built after it.
	opts = append(
		[]graph.Option{graph.ConcurrencyLimit(co.Parallelism.ItemFetch)},
		opts...)

	s, err := NewService(creds, counter, opts...)
	if err != nil {
		return Client{}, err
	}

	li, err := newLargeItemService(creds, counter, opts...)
	if err != nil {
		return Client{}, err
	}

	azureAuth, err := graph.NewAzureAuth(creds)
	if err != nil {
		return Client{}, clues.Wrap(err, "generating azure authorizer")
	}

	// The Requester runs on graph.NewHTTPWrapper's own middleware chain
	// (graph.internalMiddleware), which has never carried a concurrency
	// limiter - upstream included - so graph.ConcurrencyLimit is deliberately
	// not passed here rather than passed and silently ignored.  Bounding
	// arbitrary-URL and large-item content downloads is a throughput decision
	// this chunk does not make; see internalMiddleware's comment.
	rqr := graph.NewNoTimeoutHTTPWrapper(
		counter,
		graph.AuthorizeRequester(azureAuth))

	if co.DeltaPageSize < 1 || co.DeltaPageSize > maxDeltaPageSize {
		co.DeltaPageSize = maxDeltaPageSize
	}

	cli := Client{
		Credentials: creds,
		Stable:      s,
		LargeItem:   li,
		Requester:   rqr,
		counter:     counter,
		options:     co,
	}

	return cli, nil
}

// Service generates a new graph servicer.  New servicers are used for paged
// and other long-running requests instead of the client's stable service,
// so that in-flight state within the adapter doesn't get clobbered.
// Most calls should use the Client.Stable property instead of calling this
// func, unless it is explicitly necessary.
func (c Client) Service(
	counter *count.Bus,
	opts ...graph.Option,
) (graph.Servicer, error) {
	return NewService(c.Credentials, counter, opts...)
}

func NewService(
	creds account.M365Config,
	counter *count.Bus,
	opts ...graph.Option,
) (*graph.Service, error) {
	a, err := graph.CreateAdapter(
		creds.AzureTenantID,
		creds.AzureClientID,
		creds.AzureClientSecret,
		counter,
		opts...)
	if err != nil {
		return nil, clues.Wrap(err, "generating graph api adapter")
	}

	return graph.NewService(a), nil
}

func newLargeItemService(
	creds account.M365Config,
	counter *count.Bus,
	opts ...graph.Option,
) (*graph.Service, error) {
	// Every option the caller gave NewClient is forwarded here, not just the
	// concurrency limit, so an api.Client's two kiota services agree on
	// retries, backoff and capacity instead of only the first one honouring
	// them. Before this chunk, newLargeItemService took no options at all and
	// silently dropped its caller's; narrowing it to the concurrency option
	// alone would instead mean an explicit graph.ConcurrencyLimit applied to
	// Stable and not to LargeItem - reintroducing, inside a single
	// api.Client, exactly the "two clients disagree about capacity" split
	// this chunk exists to remove. Client.Service() and NewService already
	// forward options; this was the one constructor that did not.
	//
	// NoTimeout() is appended *last* and so always wins: a caller cannot take
	// the no-timeout property away from the large-item client, which is the
	// only reason that client exists. Pinned at this call site by
	// TestNewClient_LargeItemKeepsNoTimeoutOverACallerTimeout, which builds a
	// client with graph.Timeout and reads the timeout off LargeItem's actual
	// http.Client; graph's TestPopulateConfig_LastOptionWins pins only the
	// option-ordering semantics this relies on, and on its own would stay
	// green if NoTimeout() were moved to the front of this slice.
	//
	// A fresh slice, not append(opts, ...), so this call can never write into
	// spare capacity in its caller's own opts backing array.
	liOpts := make([]graph.Option, 0, len(opts)+1)
	liOpts = append(liOpts, opts...)
	liOpts = append(liOpts, graph.NoTimeout())

	a, err := NewService(creds, counter, liOpts...)
	return a, clues.Wrap(err, "generating no-timeout graph adapter").OrNil()
}

type Getter interface {
	Get(
		ctx context.Context,
		url string,
		headers map[string]string,
		requireAuth bool,
	) (*http.Response, error)
}

// Get performs an ad-hoc get request using its graph.Requester
func (c Client) Get(
	ctx context.Context,
	url string,
	headers map[string]string,
	requireAuth bool,
) (*http.Response, error) {
	return c.Requester.Request(ctx, http.MethodGet, url, nil, headers, requireAuth)
}

// Get performs an ad-hoc get request using its graph.Requester
func (c Client) Post(
	ctx context.Context,
	url string,
	headers map[string]string,
	body io.Reader,
	requireAuth bool,
) (*http.Response, error) {
	return c.Requester.Request(ctx, http.MethodGet, url, body, headers, requireAuth)
}

// ---------------------------------------------------------------------------
// per-call config
// ---------------------------------------------------------------------------

// CallConfig defines additional parameters to add to the query like field
// selection and limiting results.
//
// Not all calls may support all options. Check individual implementations to
// see what's supported and what's not.
type CallConfig struct {
	Expand              []string
	Select              []string
	CanMakeDeltaQueries bool
	// LimitResults limits the returned results to the given number. If 0, returns
	// all results.
	LimitResults int
}

// ---------------------------------------------------------------------------
// common interfaces
// ---------------------------------------------------------------------------

type GetByIDer[T any] interface {
	GetByID(
		ctx context.Context,
		identifier string,
		cc CallConfig,
	) (T, error)
}
