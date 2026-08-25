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

// entryPoints is every package-level function a caller outside the package is
// meant to call. It is the list the package comment names.
var entryPoints = []string{
	"Connect",
	"LoadConfig",
	"New",
	"NewLogger",
	"NewNotifier",
	"Usage",
}

// TestExportedFunctionsAreTheEntryPoints keeps the package's interface no wider
// than what a caller crosses.
//
// A function that is exported but called only from inside costs a reader the
// work of ruling it out, and costs the package comment the right to say what it
// says. Exporting a new one means either adding it here, along with the reason a
// caller needs it, or leaving it unexported.
func TestExportedFunctionsAreTheEntryPoints(t *testing.T) {
	got := exportedFunctions(t)

	want := append([]string(nil), entryPoints...)
	sort.Strings(want)

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("package-level exported functions:\n got %v\nwant %v", got, want)
	}
}

// exportedFunctions parses the package's own source and returns the names of its
// exported package-level functions, sorted. Methods are left out: they are
// reached through a type a caller already holds, not through the package.
func exportedFunctions(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read the package directory: %v", err)
	}

	fset := token.NewFileSet()

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.IsExported() {
				names = append(names, fn.Name.Name)
			}
		}
	}
	sort.Strings(names)

	return names
}
