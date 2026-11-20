package admin

import (
	"testing"

	"github.com/tsawler/cms/content"
)

func treePtr(id int64) *int64 { return &id }

// The editor's menu tree must hold every child, whatever order its row
// arrives in: the editor saves the whole tree back through ReplaceMenu, so
// a child missing from what it loaded is deleted by the next save.
func TestMenuTreeChildBeforeParent(t *testing.T) {
	items := []content.MenuItem{
		{ID: 1, Menu: "main", Sort: 0, Label: "Home", URL: "/"},
		{ID: 4, Menu: "main", Sort: 0, Label: "Batteries", URL: "/batteries", ParentID: treePtr(3)},
		{ID: 5, Menu: "main", Sort: 1, Label: "Paint", URL: "/paint", ParentID: treePtr(3)},
		{ID: 3, Menu: "main", Sort: 2, Label: "Programs"},
	}
	out := menuTree(items)
	if len(out) != 2 {
		t.Fatalf("top level = %d items %+v, want 2", len(out), out)
	}
	if out[0].Label != "Home" || out[0].Dropdown {
		t.Errorf("first item = %+v", out[0])
	}
	p := out[1]
	if p.Label != "Programs" || !p.Dropdown || len(p.Children) != 2 ||
		p.Children[0].Label != "Batteries" || p.Children[1].Label != "Paint" {
		t.Errorf("dropdown = %+v, want Programs holding Batteries then Paint", p)
	}
}

// What the editor loads for a ReplaceMenu-shaped menu is unchanged, and a
// grandchild is still left out (menus are one level deep).
func TestMenuTreeReplaceMenuLayout(t *testing.T) {
	page := int64(7)
	items := []content.MenuItem{
		{ID: 10, Menu: "main", Sort: 0, Label: "Company"},
		{ID: 11, Menu: "main", Sort: 1, Label: "About", PageID: &page, ParentID: treePtr(10)},
		{ID: 12, Menu: "main", Sort: 2, Label: "Jobs", URL: "/jobs", ParentID: treePtr(10)},
		{ID: 13, Menu: "main", Sort: 3, Label: "Deep", URL: "/deep", ParentID: treePtr(12)},
		{ID: 14, Menu: "main", Sort: 4, Label: "Contact", URL: "/contact"},
	}
	out := menuTree(items)
	if len(out) != 2 || out[0].Label != "Company" || out[1].Label != "Contact" {
		t.Fatalf("top level = %+v", out)
	}
	kids := out[0].Children
	if len(kids) != 2 || kids[0].Label != "About" || kids[0].PageID != 7 || kids[1].Label != "Jobs" {
		t.Errorf("children = %+v, want About (page 7) then Jobs", kids)
	}
}
