package storage

import (
	"encoding/json"
	"fmt"
)

// String implements fmt.Stringer, covering the %v, %+v, %s and %q verbs.
// Storage.Config is a plain map[string]string that, depending on
// Provider, holds the repository's own encryption key
// (CommonConfig.CorsoPassphrase) and, for S3-compatible storage, an
// access key and secret key. Without this, every one of those verbs
// printed Config in full. Callers needing the real values should use
// ToS3Config/ToFilesystemConfig/CommonConfig directly - this type's own
// default formatting never includes them.
func (s Storage) String() string {
	return fmt.Sprintf("storage.Storage{Provider: %s, Config: <redacted>}", s.Provider)
}

// GoString implements fmt.GoStringer, covering %#v specifically: fmt
// checks GoStringer separately from Stringer for that verb.
func (s Storage) GoString() string {
	return s.String()
}

// MarshalJSON implements json.Marshaler. encoding/json does not consult
// Stringer (or any other fmt interface) at all, so without this,
// json.Marshal(s) - or marshalling any struct that embeds or contains a
// Storage field, directly or several layers down - emits Config, and so
// the repository's encryption key and any S3 credentials, in plain text.
// This matters beyond logging: a payload persisted to a job queue or any
// other store that round-trips through JSON carries this struct's fields
// as-is, with no special handling for this one.
func (s Storage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Provider string `json:"provider"`
		Config   string `json:"config"`
	}{
		Provider: s.Provider.String(),
		Config:   "<redacted>",
	})
}
