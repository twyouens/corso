package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEngineAPI_ExportedSignaturesNameNoInternalType is chunk 0.7's half
// of the repo-wide internal-import convention (see the fork's CLAUDE.md
// and src/cmd/backupexample/import_test.go). It extends 0.6's check from
// a different angle, because 0.6's own review established that the two
// guard different things and that neither is sufficient alone:
//
//   - backupexample's import_test proves the *consuming example* writes
//     no internal import. That catches someone reaching into
//     src/internal to make up for a gap in pkg/engine. It says nothing
//     about pkg/engine's own exported surface, because pkg/engine
//     legitimately imports internal packages (internal/model,
//     internal/events, internal/version) for its implementation.
//   - This test proves pkg/engine never *names* one of those internal
//     types in an exported signature. That is the property that actually
//     makes the package usable from k3ep: Go only forbids the import, so
//     an exported function returning an internal type compiles fine here
//     and leaves a caller in another module able to call it but unable to
//     write down what it got (the "type-inference escape hatch" this
//     package's doc comment describes). A reviewer reading a diff would
//     have to notice that by eye; this notices it mechanically.
//
// What this does NOT catch, by construction - stated plainly, as 0.6's
// equivalent does, since both are now conventions later chunks will copy:
//
//   - It is syntactic, not type-checked. It flags a qualified identifier
//     whose package qualifier resolves to an internal import, and a local
//     type name that is (transitively) declared as an internal type. It
//     does not run the type checker, so a type reached only through
//     inference - an exported function whose result type is written as a
//     call to another function, say - is invisible to it.
//   - It only reads this one directory's non-test files.
//   - Generic type-parameter constraints ARE inspected, on both
//     functions and types, since a caller must satisfy them. That was a
//     gap review found and is closed; the package uses no generics
//     today, so the coverage is latent but real.
//   - A build-tag-excluded file is still parsed (parser.ParseFile ignores
//     build constraints), which here errs on the safe side.
//   - It says nothing about whether the *values* an exported signature
//     carries are built from internal types underneath. A struct field
//     of type map[string]string is fine no matter what filled it.
//
// The guarantee k3ep actually relies on remains Go's own cross-module
// internal rule, exercised by compiling an out-of-module program against
// this package (0.6's criterion 1). This test is the fast, in-repo
// regression guard for the thing that rule turns into a compile error.
func TestEngineAPI_ExportedSignaturesNameNoInternalType(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected at least one .go file in this directory")

	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, err, "parsing %s", file)

		parsed[file] = f
	}

	require.NotEmpty(t, parsed, "expected at least one non-test .go file")

	// internalQualifiers are the package qualifiers, per file, that refer
	// to an internal package. Per-file because an import alias is
	// file-scoped.
	internalQualifiers := map[string]map[string]string{}

	for file, f := range parsed {
		quals := map[string]string{}

		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)

			if !isInternalPath(path) {
				continue
			}

			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}

			// A dot import of an internal package would put internal type
			// names into this file's scope unqualified, where no amount of
			// qualifier matching would find them. Refuse outright.
			assert.NotEqual(t, ".", name,
				"%s dot-imports internal package %q; an exported signature could then "+
					"name an internal type with no qualifier for this test to catch", file, path)

			quals[name] = path
		}

		internalQualifiers[file] = quals
	}

	// tainted is the set of this package's own type names that resolve to
	// an internal type, so that an exported signature cannot launder one
	// through a local alias ("type foo = model.StableID"). Computed to a
	// fixed point, so a chain of local aliases is caught too.
	tainted := map[string]string{}

	for changed := true; changed; {
		changed = false

		for file, f := range parsed {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}

				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}

					if _, already := tainted[ts.Name.Name]; already {
						continue
					}

					// Only the type's own externally-nameable surface
					// taints it. An unexported struct field or method of
					// an internal type is exactly the implementation
					// detail pkg/engine is allowed - and designed - to
					// keep internal (Repository.repo is one), so it must
					// not make the whole type unusable.
					if why := typeSpecInternalRef(ts, internalQualifiers[file], tainted); why != "" {
						tainted[ts.Name.Name] = why
						changed = true
					}
				}
			}
		}
	}

	checkedSignatures := 0

	for file, f := range parsed {
		quals := internalQualifiers[file]

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !exportedFunc(d) {
					continue
				}

				checkedSignatures++

				// A method's receiver counts: a method on an exported
				// type whose receiver is internal would be unusable too.
				if d.Recv != nil {
					for _, fld := range d.Recv.List {
						assertNoInternal(t, file, d.Name.Name+" receiver", fld.Type, quals, tainted)
					}
				}

				// A generic function's type-parameter CONSTRAINTS are
				// part of its exported signature: a caller in another
				// module has to satisfy them, so naming an internal type
				// there is as unusable as naming one in a parameter.
				// Added after review confirmed
				// `func ProbeH[T model.StableID](v T) string` passed the
				// earlier version of this check clean.
				assertNoInternalFields(t, file, d.Name.Name+" type parameter", d.Type.TypeParams, quals, tainted)
				assertNoInternalFields(t, file, d.Name.Name+" parameter", d.Type.Params, quals, tainted)
				assertNoInternalFields(t, file, d.Name.Name+" result", d.Type.Results, quals, tainted)

			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}

						checkedSignatures++

						assertExportedTypeIsClean(t, file, s, quals, tainted)

					case *ast.ValueSpec:
						exported := false

						for _, n := range s.Names {
							if n.IsExported() {
								exported = true
							}
						}

						if !exported || s.Type == nil {
							continue
						}

						checkedSignatures++

						assertNoInternal(t, file, "exported value", s.Type, quals, tainted)
					}
				}
			}
		}
	}

	// A guard on the guard: if a refactor ever moved every exported name
	// out of this package, the loop above would pass by checking nothing.
	assert.Greater(t, checkedSignatures, 20,
		"expected to check pkg/engine's whole exported surface; found only %d declarations",
		checkedSignatures)
}

func isInternalPath(path string) bool {
	return path == "internal" ||
		strings.HasSuffix(path, "/internal") ||
		strings.Contains(path, "/internal/")
}

// exportedFunc reports whether fd is reachable by a caller outside this
// package: an exported function, or an exported method on an exported
// type.
func exportedFunc(fd *ast.FuncDecl) bool {
	if !fd.Name.IsExported() {
		return false
	}

	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return true
	}

	return receiverBaseIsExported(fd.Recv.List[0].Type)
}

func receiverBaseIsExported(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverBaseIsExported(e.X)
	case *ast.Ident:
		return e.IsExported()
	case *ast.IndexExpr: // generic receiver
		return receiverBaseIsExported(e.X)
	}

	return false
}

// nameableParts returns the parts of a type declaration a caller
// outside this package can actually name, each with a label: for a
// struct its exported and embedded fields (an unexported field is an
// implementation detail no caller can write down), for an interface its
// exported and embedded methods, and for anything else - an alias, a
// defined type over another type, a named map or slice - the whole type
// expression.
func nameableParts(ts *ast.TypeSpec) map[string]ast.Expr {
	parts := map[string]ast.Expr{}

	switch u := ts.Type.(type) {
	case *ast.StructType:
		for i, fld := range u.Fields.List {
			// An embedded field has no name and is part of the exported
			// surface: its own methods and fields are promoted.
			if len(fld.Names) == 0 {
				parts["embedded field "+strconv.Itoa(i)] = fld.Type
				continue
			}

			for _, n := range fld.Names {
				if n.IsExported() {
					parts["field "+n.Name] = fld.Type
				}
			}
		}

	case *ast.InterfaceType:
		for i, m := range u.Methods.List {
			if len(m.Names) == 0 {
				parts["embedded interface "+strconv.Itoa(i)] = m.Type
				continue
			}

			for _, n := range m.Names {
				if n.IsExported() {
					parts["method "+n.Name] = m.Type
				}
			}
		}

	default:
		parts[""] = ts.Type
	}

	return parts
}

// typeSpecInternalRef returns a description of the first internal type
// ts names in its externally-nameable surface, or "".
func typeSpecInternalRef(ts *ast.TypeSpec, quals, tainted map[string]string) string {
	for _, expr := range nameableParts(ts) {
		if why := internalRef(expr, quals, tainted); why != "" {
			return why
		}
	}

	for _, expr := range typeParamParts(ts.TypeParams) {
		if why := internalRef(expr, quals, tainted); why != "" {
			return why
		}
	}

	return ""
}

// typeParamParts returns a generic declaration's type-parameter
// constraints, which are part of its exported signature.
func typeParamParts(tp *ast.FieldList) map[string]ast.Expr {
	parts := map[string]ast.Expr{}

	if tp == nil {
		return parts
	}

	for i, fld := range tp.List {
		label := "type parameter " + strconv.Itoa(i)
		if len(fld.Names) > 0 {
			label = "type parameter " + fld.Names[0].Name
		}

		parts[label] = fld.Type
	}

	return parts
}

func assertExportedTypeIsClean(
	t *testing.T,
	file string,
	ts *ast.TypeSpec,
	quals map[string]string,
	tainted map[string]string,
) {
	t.Helper()

	parts := nameableParts(ts)

	for label, expr := range typeParamParts(ts.TypeParams) {
		parts[label] = expr
	}

	for label, expr := range parts {
		what := "type " + ts.Name.Name
		if len(label) > 0 {
			what += " " + label
		}

		assertNoInternal(t, file, what, expr, quals, tainted)
	}
}

func assertNoInternalFields(
	t *testing.T,
	file, what string,
	fields *ast.FieldList,
	quals map[string]string,
	tainted map[string]string,
) {
	t.Helper()

	if fields == nil {
		return
	}

	for _, fld := range fields.List {
		assertNoInternal(t, file, what, fld.Type, quals, tainted)
	}
}

func assertNoInternal(
	t *testing.T,
	file, what string,
	expr ast.Expr,
	quals map[string]string,
	tainted map[string]string,
) {
	t.Helper()

	if why := internalRef(expr, quals, tainted); why != "" {
		t.Errorf("%s: %s names internal type %s - pkg/engine's exported surface must use only "+
			"plain Go types or other src/pkg types, or a caller in another module cannot name it",
			file, what, why)
	}
}

// internalRef walks a type expression and returns a description of the
// first internal type it names, or "" if it names none.
func internalRef(expr ast.Expr, quals map[string]string, tainted map[string]string) string {
	found := ""

	ast.Inspect(expr, func(n ast.Node) bool {
		if found != "" {
			return false
		}

		switch e := n.(type) {
		case *ast.SelectorExpr:
			id, ok := e.X.(*ast.Ident)
			if !ok {
				return true
			}

			if path, isInternal := quals[id.Name]; isInternal {
				found = path + "." + e.Sel.Name
				return false
			}

			// A qualified name's package qualifier is not a type; do not
			// descend into it looking for a local identifier.
			return false

		case *ast.Ident:
			if why, isTainted := tainted[e.Name]; isTainted {
				found = e.Name + " (declared as " + why + ")"
				return false
			}
		}

		return true
	})

	return found
}
