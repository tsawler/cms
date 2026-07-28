package migrations

import (
	"io/fs"
	"testing"
)

// TestDialectsShareOneVersionSequence guards the invariant the package doc
// promises: version N means the same change on every engine. A migration
// added to one directory and forgotten in the other would otherwise only
// surface as a schema drift much later, on whichever engine was skipped.
func TestDialectsShareOneVersionSequence(t *testing.T) {
	versions := map[string]map[int]string{}
	for _, dir := range []string{"postgres", "mysql"} {
		entries, err := fs.ReadDir(sqlFS, "sql/"+dir)
		if err != nil {
			t.Fatalf("reading sql/%s: %v", dir, err)
		}
		versions[dir] = map[int]string{}
		for _, e := range entries {
			v, err := versionOf(e.Name())
			if err != nil {
				t.Fatalf("sql/%s/%s: %v", dir, e.Name(), err)
			}
			if prev, dup := versions[dir][v]; dup {
				t.Errorf("sql/%s: version %d used by both %s and %s", dir, v, prev, e.Name())
			}
			versions[dir][v] = e.Name()
		}
		if len(versions[dir]) == 0 {
			t.Fatalf("sql/%s holds no migrations", dir)
		}
	}

	for v, name := range versions["postgres"] {
		if _, ok := versions["mysql"][v]; !ok {
			t.Errorf("version %d exists as sql/postgres/%s but has no sql/mysql counterpart", v, name)
		}
	}
	for v, name := range versions["mysql"] {
		if _, ok := versions["postgres"][v]; !ok {
			t.Errorf("version %d exists as sql/mysql/%s but has no sql/postgres counterpart", v, name)
		}
	}

	// The sequence should have no gaps, so "latest version" and "number of
	// migrations" stay the same thing.
	for i := 1; i <= len(versions["postgres"]); i++ {
		if _, ok := versions["postgres"][i]; !ok {
			t.Errorf("version %d is missing: the sequence has a gap", i)
		}
	}
}
