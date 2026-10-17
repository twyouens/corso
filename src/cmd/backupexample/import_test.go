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
// every .go file in this directory (excluding this test file itself) and
// fails if any import path contains "/internal/" or ends in "/internal".
//
// Go's own compiler does not stop this program from importing an internal
// package - cmd/backupexample shares a module with src/internal/..., so
// the internal-import restriction that actually matters to k3ep (a
// different module entirely) would not fire here. This test is what plays
// that role instead: if pkg/engine (or any future addition to this
// program) ever stopped being enough and someone reached into
// src/internal to make up the difference, this test - not a human
// reviewer - catches it.
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
