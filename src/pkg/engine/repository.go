package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/twyouens/corso/src/internal/events"
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
//
// Trust model: this package trusts RepositoryConfig completely and
// enforces almost nothing about where Storage points or what Account
// authenticates as (beyond ErrMissingStorageCredentials below) - it
// cannot know, and does not try to guess, whether a given value is
// correct for the caller's own tenant. Concretely, and by review: this
// package gives a caller holding (or guessing) a storage location and key
// an existence-and-key oracle (its errors distinguish "wrong password"
// from "not initialized"), an arbitrary-write primitive at any filesystem
// path the caller can reach (InitializeRepository creates it, files
// included, if nothing is there yet), and an unvalidated outbound
// connection to any S3-compatible endpoint it is given, including an
// internal or loopback address, with the responder's own error body
// reflected back. None of that is a bug in this package to fix - it is
// the direct consequence of "the caller supplies everything" (REPO-3),
// which k3ep needs to open its own tenants' own repositories. It is k3ep's
// obligation, not this package's, to derive Storage's location and
// credentials and Account's identity entirely server-side, before a
// tenant-influenced value could ever reach this struct's fields.
//
// Tags are not an authorization boundary either: a single Repository
// handle (and the storage location/key behind it) must hold exactly one
// tenant's data for tag-based filtering (BackupRequest.Tags,
// ListBackupPoints, DeleteBackupPoints) to be a meaningful boundary at
// all. This package enforces the matching logic correctly - see
// store.Tags and BackupOperation.AdditionalTags - but has no concept of
// "my tenant" to check tags against; that separation is k3ep's to keep by
// construction (one repository per tenant), not something a tag value can
// create on its own.
//
// Never log, print or format a RepositoryConfig (or a Storage or Account
// value) with the standard library's default formatting: Storage and
// Account each carry a credential (the repository's own encryption key,
// and the Microsoft 365 application's client secret) in a
// map[string]string field with no special treatment from fmt. This type's
// String, GoString and LogValue methods (redact.go) exist exactly to make
// %v/%+v/%#v/%s and slog logging safe; nothing else here does. Separately,
// and by design of the fork underneath this package rather than anything
// pkg/engine adds: with no logger configured, the fork's own singleton
// logger writes info-level logs to the process user's home directory, and
// clues.Hide(...)-wrapped values (such as a tenant or resource owner ID
// this package's own ctx-building does not add, but the fork's lower
// layers do) go out in plaintext unless the caller's own logging setup
// configures PII hashing. This package cannot change that default without
// changing the CLI's own logging behavior, which is out of scope here;
// k3ep must configure its own logger and PII handling before relying on
// this package in a context subject to retention or access-control
// requirements.
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
	//
	// For S3-compatible storage, AccessKey and SecretKey are required:
	// this package rejects an S3 Storage with either empty outright (see
	// ErrMissingStorageCredentials) rather than letting Kopia fall
	// through to the platform's own ambient AWS identity.
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
// Calling this again at a configuration that already has a repository is
// safe as long as cfg.Storage's CorsoPassphrase is the same one the
// repository was created with: the fork's own Initialize treats that as a
// reconnect, not an error, so this returns the same repository (same ID)
// rather than failing - confirmed live against the test tenant. It is
// ErrRepositoryExists (wrapping the fork's own
// repository.ErrorRepoAlreadyExists) only when the repository already
// exists AND the connection that follows fails - in practice, a wrong
// CorsoPassphrase for an existing repository. OpenRepository is still the
// right call when the caller already knows a repository exists; this
// behavior exists so a caller unsure whether it does can use
// InitializeRepository either way without special-casing "already
// exists".
func InitializeRepository(ctx context.Context, cfg RepositoryConfig) (*Repository, error) {
	// repository.NewRepoID ("") tells the fork to mint a brand-new random
	// ID for this, the first-ever connection to this repository.
	repo, counter, err := newRepo(ctx, cfg, repository.NewRepoID)
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
	// events.RepoIDNotFound is the fork's own sentinel (distinct from the
	// empty string used above) telling setupKopia this caller does not
	// already know the repository's ID, so it must read the real one back
	// from the repository's own stored model instead of trusting whatever
	// was passed in. Passing "" here (as the CLI's own config-file-less
	// path never does - see cli/repo/filesystem.go) would instead leave
	// the ID at a freshly minted random value every single call, so two
	// Repository handles opened against the exact same repository would
	// each report a different, meaningless ID. Confirmed live against the
	// test tenant: this was the before-fix behavior, caught by a round
	// trip test asserting the ID survives Close+OpenRepository.
	repo, counter, err := newRepo(ctx, cfg, events.RepoIDNotFound)
	if err != nil {
		return nil, err
	}

	if err := repo.Connect(ctx, repository.ConnConfig{}); err != nil {
		return nil, fmt.Errorf("engine: connecting to repository: %w", err)
	}

	return &Repository{repo: repo, counter: counter}, nil
}

func newRepo(ctx context.Context, cfg RepositoryConfig, repoID string) (repository.Repositoryer, *count.Bus, error) {
	if err := validateStorageCredentials(cfg.Storage); err != nil {
		return nil, nil, err
	}

	ensureConcurrencyLimiterInitialized(ctx, cfg.Options)

	repo, err := repository.New(
		ctx,
		cfg.Account,
		cfg.Storage,
		cfg.Options,
		repoID)
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
