package janitor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// entryPoints is every name the package exports at package level, and it is the
// list the package comment names.
//
// The six functions and the five types in their signatures are what a caller
// crosses. The rest are exported for a reason recorded beside them; each is a
// name a caller cannot reach today, kept only because unexporting it belongs to
// work this list is not.
var entryPoints = []string{
	// What a command calls.
	"Connect",
	"LoadConfig",
	"New",
	"NewLogger",
	"NewNotifier",
	"Usage",

	// What those six pass and return.
	"Cluster",
	"Config",
	"Janitor",
	"Logger",
	"Notifier",

	// Reached only through Config, which a caller does hold: a Rule comes from
	// Config.Rules, and Rule.Matches takes a Target.
	"Rule",
	"Target",

	// The Resource context and hook candidates from the architecture review
	// restructure these; unexporting them first would only be undone.
	"ResourceContext",
	"ResourceContextHook",

	// The annotation keys a resource carries. The strings are the contract a
	// user writes; the names are not, and nothing outside reads them.
	"ExpiryAnnotation",
	"NotifiedAnnotation",
	"TTLAnnotation",
	"TTLUnlimited",
}

// TestExportedNamesAreTheEntryPoints keeps the package's interface no wider than
// what a caller crosses.
//
// A name that is exported but reached only from inside costs a reader the work
// of ruling it out, and costs the package comment the right to say what it says.
// Exporting a new one means either adding it above, with the reason a caller
// needs it, or leaving it unexported.
func TestExportedNamesAreTheEntryPoints(t *testing.T) {
	got := exportedNames(t)

	want := append([]string(nil), entryPoints...)
	sort.Strings(want)

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("package-level exported names:\n got %v\nwant %v", got, want)
	}
}

// exportedNames parses the package's own source and returns every exported
// package-level name — function, type, constant or variable — sorted. Methods
// are left out: they are reached through a type a caller already holds, not
// through the package.
func exportedNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()

	var names []string
	for _, name := range sourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			names = append(names, declaredNames(decl)...)
		}
	}
	sort.Strings(names)

	return names
}

// declaredNames returns the exported names one declaration introduces at package
// level.
func declaredNames(decl ast.Decl) []string {
	var names []string

	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil && d.Name.IsExported() {
			names = append(names, d.Name.Name)
		}

	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if s.Name.IsExported() {
					names = append(names, s.Name.Name)
				}
			case *ast.ValueSpec:
				for _, ident := range s.Names {
					if ident.IsExported() {
						names = append(names, ident.Name)
					}
				}
			}
		}
	}

	return names
}

// sourceFiles names the package's own Go files, leaving out its tests and the
// files the go command itself ignores.
func sourceFiles(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read the package directory: %v", err)
	}

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		ignored := strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")

		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && !ignored {
			names = append(names, name)
		}
	}

	return names
}
