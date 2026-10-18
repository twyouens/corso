package engine

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libraryAPIDoc is the k3ep-facing guide to this package, README.md in this
// directory, embedded rather than read from disk.
//
// Embedded deliberately, and the reason is the whole point of this guard.
// The guide first lived at the repository root in docs/, outside this Go
// module, where Go's test cache did not treat it as an input: editing only
// the markdown and re-running `go test ./...` reported a cached pass. CI
// was no better - .github/workflows/ci.yml restores GOCACHE keyed only on
// hashFiles('src/go.sum'), and `make check` runs `go test ./...` with no
// -count=1, so a pull request touching only the guide would have been
// handed the same cached pass. Embedding makes the markdown part of this
// test binary, so any edit to it changes the binary's build inputs and the
// cached result cannot be reused, locally or in CI. Measured both ways
// before and after the move.
//
//go:embed README.md
var libraryAPIDoc string

// libraryAPIDocPath is only used in failure messages, so a reader knows
// which file to edit.
const libraryAPIDocPath = "src/pkg/engine/README.md"

// TestLibraryAPIDoc_MatchesExportedSurface is the documentation-integrity
// check chunk 0.8 added, and it runs in both directions:
//
//   - Nothing documented is imaginary. Every "engine.Name" the guide
//     mentions must be a real exported declaration in this package, and
//     every "Type.Member" it mentions (for a type this package exports)
//     must be a real exported field or method of that type. A renamed or
//     deleted symbol reddens this test instead of quietly leaving a lie in
//     the guide.
//   - Nothing exported is undocumented. Every exported top-level
//     declaration in this package - type, function, error value or
//     constant - must be named somewhere in the guide. A later chunk that
//     adds an exported symbol has to say what it is for, in the one
//     document k3ep developers read.
//
// What it deliberately does not do, so nobody reads more into a pass than
// is there:
//
//   - It checks names, not claims. That a field is mentioned says nothing
//     about whether the prose around it is true.
//   - It does not require every exported method or struct field to be
//     mentioned; only top-level declarations. Methods are covered from a
//     different angle: example_test.go has one godoc example per capability
//     area (ExampleRepository_Backup, ExampleRepository_ListFolders and the
//     rest), and go vet's own "tests" analyzer fails the build if such a
//     name does not match a real exported method - so the examples cannot
//     drift from the methods either.
//   - It is textual, so it cannot tell a mention in a code block from a
//     mention in prose, and cannot notice a symbol documented under the
//     wrong heading.
//   - It does not need -count=1, and that is a property of the embed, not
//     of go test: see libraryAPIDoc above for the cached-pass hole this
//     closes and how it was measured. Nothing else in this repository
//     depends on a file outside the module; anything that does should
//     assume the same hole until it proves otherwise.
func TestLibraryAPIDoc_MatchesExportedSurface(t *testing.T) {
	doc := libraryAPIDoc
	require.NotEmpty(t, doc, "the embedded guide is empty")

	topLevel, fields, methods := exportedSurface(t)

	// 1. Nothing documented is imaginary: every "engine.Name" the guide
	// mentions is a real exported declaration here.
	qualified := regexp.MustCompile(`\bengine\.([A-Za-z_][A-Za-z0-9_]*)`)

	for _, m := range qualified.FindAllStringSubmatch(doc, -1) {
		name := m[1]
		if !ast.IsExported(name) {
			continue
		}

		assert.Contains(
			t,
			topLevel,
			name,
			"%s names engine.%s, which is not an exported declaration of this package",
			libraryAPIDocPath,
			name)
	}

	// 2. Every "Type.Member" the guide mentions, for a type this package
	// exports, is a real exported field or method of it. Pairs whose first
	// element is not one of this package's types ("storage.Storage",
	// "Options.Parallelism") are someone else's surface and are skipped.
	member := regexp.MustCompile(`\b([A-Z][A-Za-z0-9]*)\.([A-Za-z][A-Za-z0-9]*)\b`)

	for _, m := range member.FindAllStringSubmatch(doc, -1) {
		typeName, memberName := m[1], m[2]

		_, isStruct := fields[typeName]
		_, hasMethods := methods[typeName]

		if !isStruct && !hasMethods {
			continue
		}

		if fields[typeName][memberName] || methods[typeName][memberName] {
			continue
		}

		assert.Fail(
			t,
			"documented member does not exist",
			"%s names %s.%s, which is neither an exported field nor a method of %s",
			libraryAPIDocPath,
			typeName,
			memberName,
			typeName)
	}

	// 3. Nothing exported is undocumented: every exported top-level
	// declaration is named somewhere in the guide.
	undocumented := []string{}

	for name := range topLevel {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(doc) {
			undocumented = append(undocumented, name)
		}
	}

	sort.Strings(undocumented)

	assert.Empty(
		t,
		undocumented,
		"these exported declarations are not named anywhere in %s: %s",
		libraryAPIDocPath,
		strings.Join(undocumented, ", "))
}

// exportedSurface parses this package's own non-test source and returns
// its exported top-level declarations, its exported struct fields by type,
// and its methods by receiver type.
func exportedSurface(t *testing.T) (
	topLevel map[string]struct{},
	fields map[string]map[string]bool,
	methods map[string]map[string]bool,
) {
	// Enumerated with Glob and parsed file by file, following
	// exported_api_test.go's pattern: go/parser.ParseDir is deprecated, and
	// like that test this one is syntactic anyway, so a build-tag-excluded
	// file is still parsed - which errs on the side of requiring more to be
	// documented, not less.
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected at least one .go file in this directory")

	fset := token.NewFileSet()
	parsed := []*ast.File{}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, err, "parsing %s", file)

		parsed = append(parsed, f)
	}

	require.NotEmpty(t, parsed, "expected at least one non-test .go file")

	topLevel = map[string]struct{}{}
	fields = map[string]map[string]bool{}
	methods = map[string]map[string]bool{}

	for _, file := range parsed {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				collectFuncDecl(d, topLevel, methods)
			case *ast.GenDecl:
				collectGenDecl(d, topLevel, fields)
			}
		}
	}

	require.NotEmpty(t, topLevel, "found no exported declarations; the parse is wrong")

	return topLevel, fields, methods
}

func collectFuncDecl(
	d *ast.FuncDecl,
	topLevel map[string]struct{},
	methods map[string]map[string]bool,
) {
	if !d.Name.IsExported() {
		return
	}

	if d.Recv == nil || len(d.Recv.List) == 0 {
		topLevel[d.Name.Name] = struct{}{}
		return
	}

	recv := d.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}

	ident, ok := recv.(*ast.Ident)
	if !ok {
		return
	}

	if methods[ident.Name] == nil {
		methods[ident.Name] = map[string]bool{}
	}

	methods[ident.Name][d.Name.Name] = true
}

func collectGenDecl(
	d *ast.GenDecl,
	topLevel map[string]struct{},
	fields map[string]map[string]bool,
) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}

			topLevel[s.Name.Name] = struct{}{}

			st, ok := s.Type.(*ast.StructType)
			if !ok {
				continue
			}

			if fields[s.Name.Name] == nil {
				fields[s.Name.Name] = map[string]bool{}
			}

			for _, f := range st.Fields.List {
				for _, n := range f.Names {
					if n.IsExported() {
						fields[s.Name.Name][n.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, n := range s.Names {
				if n.IsExported() {
					topLevel[n.Name] = struct{}{}
				}
			}
		}
	}
}
