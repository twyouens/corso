package engine

import (
	"fmt"
	"log/slog"
)

// String implements fmt.Stringer, covering the %v, %+v, %s and %q verbs:
// Go's fmt package prefers Stringer over its own default struct/map
// formatting for all of them. Without this, RepositoryConfig's embedded
// Storage.Config and Account.Config maps - which hold the repository's
// encryption key and the Microsoft 365 application's client secret
// respectively, as plain map[string]string entries with no special
// treatment from fmt - print in full under every one of those verbs. See
// RepositoryConfig's own doc comment.
func (cfg RepositoryConfig) String() string {
	return fmt.Sprintf(
		"engine.RepositoryConfig{Storage: storage.Storage{Provider: %s, Config: <redacted>}, "+
			"Account: account.Account{Provider: %s, Config: <redacted>}, Options: %+v}",
		cfg.Storage.Provider,
		cfg.Account.Provider,
		cfg.Options)
}

// GoString implements fmt.GoStringer, covering %#v specifically: fmt
// checks GoStringer separately from Stringer for that verb, so String
// above does not cover it on its own.
func (cfg RepositoryConfig) GoString() string {
	return cfg.String()
}

// LogValue implements log/slog.LogValuer, so a RepositoryConfig passed to
// slog (slog.Any("config", cfg), or embedded in a struct some other value
// logged with slog) is redacted the same way String is, rather than
// slog's default reflection-based formatting walking into Storage.Config
// and Account.Config.
func (cfg RepositoryConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("storage_provider", cfg.Storage.Provider.String()),
		slog.String("account_provider", cfg.Account.Provider.String()))
}
