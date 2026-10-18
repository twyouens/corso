package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libraryAPIDoc is the k3ep-facing guide to this package: docs/library-api.md,
// relative to this package's own directory.
const libraryAPIDoc = "../../../docs/library-api.md"

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
//   - One operational trap, measured rather than assumed: the guide lives
//     outside this Go module (the module root is src/, the guide is in the
//     repository's docs/), so Go's test cache does not treat it as an input.
//     Editing only the markdown and re-running `go test ./...` reports a
//     cached pass. After editing the guide, run
//     `GOWORK=off GO111MODULE=on go test -count=1 ./pkg/engine/`. CI, with a
//     cold cache, does run it.
func TestLibraryAPIDoc_MatchesExportedSurface(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(libraryAPIDoc))
	require.NoError(t, err, "reading %s", libraryAPIDoc)

	doc := string(raw)

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
			libraryAPIDoc,
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
			libraryAPIDoc,
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
		libraryAPIDoc,
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
	fset := token.NewFileSet()

	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing this package's source")

	pkg, ok := pkgs["engine"]
	require.True(t, ok, "package engine not found in this directory")

	topLevel = map[string]struct{}{}
	fields = map[string]map[string]bool{}
	methods = map[string]map[string]bool{}

	for _, file := range pkg.Files {
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
