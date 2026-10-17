// Package engine is the single public surface k3ep (or any other caller in
// a different Go module) uses to open an encrypted Kopia repository, back
// up Microsoft 365 data into it with caller-supplied tags, and list,
// delete or run maintenance on the resulting backup points.
//
// It exists because Go forbids importing another module's internal
// packages (REPO-2): the lower-level pkg/repository package - the fork's
// original, CLI-oriented entry point - returns several types from
// src/internal/operations (operations.BackupOperation,
// operations.MaintenanceOperation, and friends) from its own exported
// methods. A caller in another module can only use those methods' return
// values through Go's type-inference "escape hatch" (never writing the
// import, never naming the type) - which works for calling a method, but
// breaks the moment that caller wants to store the value in a struct field,
// pass it to a function with a named parameter type, or do anything else
// that requires writing the type name. That is not a deliberate public API;
// it is an accident of Go's internal-import enforcement being a property of
// import statements, not of value usage. This package is the deliberate
// promotion REPO-2 and REPO-3 call for: every exported type and function
// signature here uses only plain Go types (string, []byte, time.Time,
// map[string]string, bool) or other src/pkg types that are themselves part
// of this fork's existing public contract (pkg/account, pkg/storage,
// pkg/control, pkg/selectors). Nothing in this package's own exported API
// ever requires - or permits - a caller to name a type from
// src/internal/anything. src/cmd/backupexample is the fork's own proof of
// that: see its package-level doc comment and
// TestExample_ImportsNoInternalPackage for how that is checked
// mechanically, not just by inspection.
//
// Every setting (storage location and encryption key, Microsoft 365
// credentials, tags, progress reporting) is supplied by the caller, as the
// fork's own CLAUDE.md requires of every src/pkg entry point: this package
// reads no environment variable, config file or flag, and it never writes
// to stdout. It never logs, prints or returns a repository key, Microsoft
// 365 secret or item content; errors name objects by backup ID or tag key
// only, never by subject, filename or file content.
package engine
