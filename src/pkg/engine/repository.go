package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/repository"
	"github.com/twyouens/corso/src/pkg/storage"
)

// ErrRepositoryExists is returned by InitializeRepository when a
// repository already exists at the given storage location with the given
// key. It wraps the fork's own pkg/repository.ErrorRepoAlreadyExists, so
// errors.Is also matches that, but callers outside this module can only
// name (and so should only depend on) this exported error.
var ErrRepositoryExists = errors.New("engine: a repository already exists with that configuration")

// RepositoryConfig is everything OpenRepository and InitializeRepository
// need. The caller supplies every field explicitly; this package never
// reads an environment variable, config file or flag to fill one in.
type RepositoryConfig struct {
	// Storage names the repository's storage destination - a mounted
	// filesystem path or an S3-compatible bucket (for example MinIO) -
	// built with storage.NewStorage. Its CommonConfig.CorsoPassphrase is
	// the repository's own encryption key (ENC-1's tenant data key, in
	// k3ep's terms): this package never generates, defaults or persists
	// that value itself, and never logs, prints or returns it. Losing it
	// means losing the repository; k3ep's own key management (wrapping it
	// with a platform master key, ENC-1) happens entirely above this
	// package.
	Storage storage.Storage

	// Account carries the Microsoft 365 application credentials this
	// repository's backups will use to call Graph. Required for both
	// Initialize and Open - see their doc comments for why this package
	// cannot make that optional - and for every call to (*Repository).Backup.
	Account account.Account

	// Options are passed straight through to the fork's operation layer
	// (parallelism, delta page size, feature toggles). Pass
	// control.DefaultOptions() for the fork's own defaults; this package
	// supplies none of its own.
	Options control.Options
}

// Repository is an open handle to a Kopia repository. Obtain one with
// InitializeRepository (first use) or OpenRepository (every later use),
// and release it with Close when done.
type Repository struct {
	repo    repository.Repositoryer
	counter *count.Bus
}

// ID returns the repository's own stable identifier. Never a secret:
// callers can log or store this freely.
func (r *Repository) ID() string {
	return r.repo.GetID()
}

// InitializeRepository creates a brand-new repository at cfg.Storage,
// encrypted with cfg.Storage's CorsoPassphrase, and connects to it.
//
// Like every repository operation in the underlying fork, this also
// connects to Microsoft 365 using cfg.Account and verifies that connection
// (fetches a real access token) before touching the storage backend - the
// fork's pkg/repository ties a repository's lifecycle to a verified M365
// account unconditionally, which happens to fit k3ep's own domain model
// (ENC-1: a repository belongs to exactly one tenant's M365 connection)
// exactly, so this package does not try to route around it. A test that
// only cares about the storage round trip still needs a valid cfg.Account.
//
// Returns ErrRepositoryExists (wrapping the fork's own
// repository.ErrorRepoAlreadyExists) if a repository already exists at
// this configuration; call OpenRepository instead.
func InitializeRepository(ctx context.Context, cfg RepositoryConfig) (*Repository, error) {
	repo, counter, err := newRepo(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := repo.Initialize(ctx, repository.InitConfig{}); err != nil {
		if errors.Is(err, repository.ErrorRepoAlreadyExists) {
			return nil, fmt.Errorf("%w: %w", ErrRepositoryExists, err)
		}

		return nil, fmt.Errorf("engine: initializing repository: %w", err)
	}

	return &Repository{repo: repo, counter: counter}, nil
}

// OpenRepository connects to a repository previously created with
// InitializeRepository, at the same cfg.Storage location and with the same
// CorsoPassphrase key. See InitializeRepository's doc comment for why
// cfg.Account is required even for a storage-only use.
func OpenRepository(ctx context.Context, cfg RepositoryConfig) (*Repository, error) {
	repo, counter, err := newRepo(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := repo.Connect(ctx, repository.ConnConfig{}); err != nil {
		return nil, fmt.Errorf("engine: connecting to repository: %w", err)
	}

	return &Repository{repo: repo, counter: counter}, nil
}

func newRepo(ctx context.Context, cfg RepositoryConfig) (repository.Repositoryer, *count.Bus, error) {
	repo, err := repository.New(
		ctx,
		cfg.Account,
		cfg.Storage,
		cfg.Options,
		repository.NewRepoID)
	if err != nil {
		return nil, nil, fmt.Errorf("engine: constructing repository: %w", err)
	}

	return repo, repo.Counter(), nil
}

// Close releases the repository handle. Safe to call once after a
// successful InitializeRepository or OpenRepository call; it does not
// affect the underlying stored data.
func (r *Repository) Close(ctx context.Context) error {
	if err := r.repo.Close(ctx); err != nil {
		return fmt.Errorf("engine: closing repository: %w", err)
	}

	return nil
}
