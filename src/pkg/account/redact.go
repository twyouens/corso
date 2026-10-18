package account

import (
	"encoding/json"
	"fmt"
)

// String implements fmt.Stringer, covering the %v, %+v, %s and %q verbs.
// Account.Config is a plain map[string]string that holds the Microsoft
// 365 application's client secret (among other, non-secret fields).
// Without this, every one of those verbs printed Config in full. Callers
// needing the real values should use M365Config directly - this type's
// own default formatting never includes them.
func (a Account) String() string {
	return fmt.Sprintf("account.Account{Provider: %s, Config: <redacted>}", a.Provider)
}

// GoString implements fmt.GoStringer, covering %#v specifically: fmt
// checks GoStringer separately from Stringer for that verb.
func (a Account) GoString() string {
	return a.String()
}

// MarshalJSON implements json.Marshaler. encoding/json does not consult
// Stringer (or any other fmt interface) at all, so without this,
// json.Marshal(a) - or marshalling any struct that embeds or contains an
// Account field, directly or several layers down - emits Config, and so
// the Microsoft 365 application's client secret, in plain text. This
// matters beyond logging: a payload persisted to a job queue or any
// other store that round-trips through JSON carries this struct's fields
// as-is, with no special handling for this one.
func (a Account) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Provider string `json:"provider"`
		Config   string `json:"config"`
	}{
		Provider: a.Provider.String(),
		Config:   "<redacted>",
	})
}
