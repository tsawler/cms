package snippets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestAllFoldsInEveryConstructor is the whole point of All(), and it has
// to be checked mechanically because the failure it guards against is
// silent: a new exported []Snippet constructor that All() does not
// return is a set of blocks no application ever sees, in an editor that
// reports no error, on sites nobody thinks to check.
//
// The check is against the package's own source rather than a hand-kept
// list here, because a hand-kept list is the same thing that went wrong.
// Adding `func Whatever() []Snippet` to this package fails this test
// until All() calls it.
func TestAllFoldsInEveryConstructor(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}

	var constructors []string
	var allBody *ast.FuncDecl
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			if fn.Name.Name == "All" {
				allBody = fn
				continue
			}
			if returnsSnippetSlice(fn) {
				constructors = append(constructors, fn.Name.Name)
			}
		}
	}
	if allBody == nil {
		t.Fatal("no exported All function in the package")
	}
	if len(constructors) == 0 {
		t.Fatal("found no []Snippet constructors — the check is not looking at anything")
	}

	called := map[string]bool{}
	ast.Inspect(allBody, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			called[id.Name] = true
		}
		return true
	})
	sort.Strings(constructors)
	for _, name := range constructors {
		if !called[name] {
			t.Errorf("All() does not call %s() — blocks it returns would reach no application. "+
				"Fold it into All in all.go.", name)
		}
	}
	t.Logf("All() covers: %s", strings.Join(constructors, ", "))
}

// returnsSnippetSlice reports whether fn takes nothing and returns
// exactly one []Snippet — the shape every block list in this package
// has. Anything else (a store method, a helper taking arguments) is not
// a library a host would be expected to name.
func returnsSnippetSlice(fn *ast.FuncDecl) bool {
	if fn.Type.Params != nil && len(fn.Type.Params.List) != 0 {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	arr, ok := fn.Type.Results.List[0].Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	id, ok := arr.Elt.(*ast.Ident)
	return ok && id.Name == "Snippet"
}

// TestAllReturnsEveryBlock is the runtime half: All() naming a
// constructor is not the same as returning what it produces.
func TestAllReturnsEveryBlock(t *testing.T) {
	want := 0
	have := map[string]bool{}
	for _, s := range All() {
		if have[s.Name] {
			// Two cards with one name in the palette is a coin flip for
			// whoever clicks it, and it means a new block quietly
			// shadowed an old one.
			t.Errorf("All() offers %q twice", s.Name)
		}
		have[s.Name] = true
	}
	for _, list := range [][]Snippet{
		DefaultSnippets(), LibrarySnippets(),
		DefaultSectionPresets(), LibrarySectionPresets(),
	} {
		for _, s := range list {
			want++
			if !have[s.Name] {
				t.Errorf("All() is missing %q", s.Name)
			}
		}
	}
	if len(All()) != want {
		t.Errorf("All() returned %d blocks, want %d", len(All()), want)
	}
}

// TestAllStartsWithTheInlineBlocks pins the order of the palette. The
// drawer shows inline blocks and section presets as two kinds of card,
// and a list that interleaves them scatters each kind through the other
// — so a new constructor appended in the wrong place is a reshuffled
// drawer on every site, which is exactly the sort of upgrade surprise
// All() exists to prevent.
func TestAllStartsWithTheInlineBlocks(t *testing.T) {
	all := All()
	firstPreset := -1
	for i, s := range all {
		if len(s.Settings) != 0 {
			firstPreset = i
			break
		}
	}
	if firstPreset < 0 {
		t.Fatal("All() has no section presets")
	}
	for _, s := range all[firstPreset:] {
		if len(s.Settings) == 0 {
			t.Errorf("inline block %q sits after the section presets — "+
				"the palette groups the two kinds, so they must not interleave", s.Name)
		}
	}
}
