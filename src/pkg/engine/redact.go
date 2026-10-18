package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/storage"
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
	// Storage and Account each implement their own Stringer (pkg/storage,
	// pkg/account), redacting their own Config maps; delegate to them
	// rather than re-redacting here, so there is exactly one place that
	// decides what a Storage or Account value may print.
	return fmt.Sprintf(
		"engine.RepositoryConfig{Storage: %s, Account: %s, Options: %+v}",
		cfg.Storage,
		cfg.Account,
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

// MarshalJSON implements json.Marshaler. encoding/json does not consult
// Stringer or LogValuer at all, so without this, json.Marshal(cfg) - or
// marshalling any struct that embeds or contains a RepositoryConfig field,
// such as a job payload persisted to a queue - would fall back to
// encoding Storage and Account as plain structs. As of this fix, Storage
// and Account (pkg/storage, pkg/account) each implement their own
// MarshalJSON too, so this override is defense in depth rather than the
// only thing standing between a RepositoryConfig and a leaked credential;
// it also exists so this type's JSON shape does not depend on whichever
// casing convention Storage's and Account's own exported field names
// happen to use.
func (cfg RepositoryConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Storage storage.Storage `json:"storage"`
		Account account.Account `json:"account"`
		Options control.Options `json:"options"`
	}{
		Storage: cfg.Storage,
		Account: cfg.Account,
		Options: cfg.Options,
	})
}
