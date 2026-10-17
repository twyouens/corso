package seed

import (
	"errors"
	"strings"

	"github.com/twyouens/corso/src/pkg/errs/core"
)

// This file holds the seeder's one idempotency mechanism: find-or-create.
// Every container and item the seeder touches goes through FindOrCreate (or
// the thin, Graph-call-shaped wrappers in mail.go/calendar.go/contacts.go/
// onedrive.go/sharepoint.go that call it). The seeder never deletes or
// recreates anything: running it twice either finds what the first run made
// and leaves it alone, or - only if something genuinely wasn't there yet -
// creates it once. There is no "clean then create" path anywhere in this
// package, for any resource, including containers that the seeder itself
// created on an earlier run.

// FindOrCreate is the seeder's idempotency primitive. find looks for an
// item that already satisfies the seed definition (typically: same name,
// same parent); if found, that exact item is reused untouched. Only when
// find reports nothing does create run. Calling FindOrCreate twice in a
// row, with a find that can see what the first call's create produced,
// is guaranteed to create nothing the second time - that guarantee is
// this package's entire repeatability story, and is exercised directly
// in upsert_test.go without touching Graph.
func FindOrCreate[T any](
	find func() (item T, found bool, err error),
	create func() (item T, err error),
) (T, bool, error) {
	existing, found, err := find()
	if err != nil {
		var zero T
		return zero, false, err
	}

	if found {
		return existing, false, nil
	}

	newItem, err := create()
	if err != nil {
		var zero T
		return zero, false, err
	}

	return newItem, true, nil
}

// ContainerNotFound reports whether err is the "no container matched that
// name" outcome of the fork's Mail/Contacts/Events GetContainerByName
// calls, as opposed to a genuine request failure that a seeding attempt
// should not paper over by trying to create a duplicate.
//
// Those three GetContainerByName implementations
// (pkg/services/m365/api/{mail,contacts,events}.go) return a filtered,
// zero-result list as a plain clues error with the message "container not
// found" - not wrapped in the exported core.ErrNotFound sentinel, because
// the underlying Graph call itself succeeded (200 OK, empty list). A real
// Graph-level 404 (e.g. a stale container ID) *is* wrapped in
// core.ErrNotFound by the graph package, so this helper checks both.
func ContainerNotFound(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, core.ErrNotFound) {
		return true
	}

	return strings.Contains(err.Error(), "container not found")
}

// AlreadyExists reports whether err is Graph's "an item with this name
// already exists in this container" conflict, as returned by
// api.Drives().PostItemInContainer with control.Skip. The seeder treats
// this the same as ContainerNotFound would have reported "found": it means
// two processes (or two runs) raced to create the same item, and whichever
// one lost should go look the item up rather than fail.
func AlreadyExists(err error) bool {
	return errors.Is(err, core.ErrAlreadyExists)
}
