package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExample_ImportsNoInternalPackage is chunk 0.6's headline acceptance
// criterion, proven mechanically rather than by inspection: this parses
// every non-test .go file directly in this directory and fails if any of
// their own import declarations names a path containing "/internal/" or
// ending in "/internal".
//
// Go's own compiler does not stop this program from importing an internal
// package - cmd/backupexample shares a module with src/internal/..., so
// the internal-import restriction that actually matters to k3ep (a
// different module entirely) would not fire here. This test is what plays
// that role instead: if pkg/engine (or any future addition to this
// program) ever stopped being enough and someone reached into
// src/internal to make up the difference, this test - not a human
// reviewer - catches it.
//
// What this does NOT catch, by construction, and any later chunk reusing
// this pattern (now a repo-wide convention; see the fork's CLAUDE.md)
// should keep in mind: it only parses this package's own direct import
// declarations.
//   - An internal type reached indirectly - through a value returned by
//     some other same-module src/pkg function, using Go's type-inference
//     "escape hatch" (see pkg/engine's own package doc comment) - without
//     this file ever writing the import itself, is invisible to this
//     check. pkg/engine is designed so that path never needs to exist;
//     this test cannot prove that design property on its own, only that
//     no one wrote the easy, visible version of the mistake.
//   - A subdirectory package under cmd/backupexample (none exists today)
//     is not scanned: filepath.Glob("*.go") is not recursive.
//   - This file's own _test.go files are excluded, deliberately: a test
//     helper is allowed to reach into internal for its own fixtures
//     without that implying the example itself needs to. A test file
//     importing internal to feed the program under test would not be
//     caught.
//   - A build-constrained file only compiled under a different
//     GOOS/GOARCH/tag, or an import reached via go:linkname (which has no
//     import declaration at all), would not be caught either.
func TestExample_ImportsNoInternalPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected at least one .go file in this directory")

	checked := 0

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		checked++

		src, err := os.ReadFile(file) //nolint:gosec
		require.NoError(t, err)

		fset := token.NewFileSet()

		f, err := parser.ParseFile(fset, file, src, parser.ImportsOnly)
		require.NoError(t, err, "parsing %s", file)

		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)

			if path == "internal" || strings.HasSuffix(path, "/internal") || strings.Contains(path, "/internal/") {
				t.Errorf("%s imports internal package %q - the example must only use src/pkg", file, path)
			}
		}
	}

	require.Greater(t, checked, 0, "expected to check at least one non-test .go file")
}
