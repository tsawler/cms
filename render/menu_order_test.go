package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// labels flattens a built menu to "Parent>Child" strings, in render order.
func menuLabels(entries []MenuEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Label)
		for _, c := range e.Children {
			out = append(out, e.Label+">"+c.Label)
		}
	}
	return out
}

func menuPtr(id int64) *int64 { return &id }

// A child whose row arrives before its parent's — numbered from zero under
// a later parent, as an importer might write it — still renders under that
// parent, and its dropdown is not dropped as empty.
func TestBuildMenusChildBeforeParent(t *testing.T) {
	items := []content.MenuItem{
		// Rows in (sort, id) order, as MenuItems returns them.
		{ID: 1, Menu: "main", Sort: 0, Label: "Home", URL: "/"},
		{ID: 4, Menu: "main", Sort: 0, Label: "Batteries", URL: "/batteries", ParentID: menuPtr(3)},
		{ID: 2, Menu: "main", Sort: 1, Label: "About", URL: "/about"},
		{ID: 5, Menu: "main", Sort: 1, Label: "Paint", URL: "/paint", ParentID: menuPtr(3)},
		{ID: 3, Menu: "main", Sort: 2, Label: "Programs"},
	}
	got := menuLabels(BuildMenus(items, "", "en", "en", false)["main"])
	want := []string{"Home", "About", "Programs", "Programs>Batteries", "Programs>Paint"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("menu = %v, want %v", got, want)
	}
}

// For a menu laid out the way ReplaceMenu writes one — a single sort
// sequence, each parent directly before its children — the two-pass build
// gives exactly the order the single-pass one did: top level in sort
// order, each dropdown's children in sort order.
func TestBuildMenusReplaceMenuLayoutUnchanged(t *testing.T) {
	items := []content.MenuItem{
		{ID: 10, Menu: "main", Sort: 0, Label: "Programs"},
		{ID: 11, Menu: "main", Sort: 1, Label: "EPR", URL: "/epr", ParentID: menuPtr(10)},
		{ID: 12, Menu: "main", Sort: 2, Label: "Tires", URL: "/tires", ParentID: menuPtr(10)},
		{ID: 13, Menu: "main", Sort: 3, Label: "Resources"},
		{ID: 14, Menu: "main", Sort: 4, Label: "Links", URL: "/links", ParentID: menuPtr(13)},
		{ID: 15, Menu: "main", Sort: 5, Label: "About", URL: "/about"},
		{ID: 16, Menu: "main", Sort: 6, Label: "Contact", URL: "/contact"},
		{ID: 20, Menu: "footer", Sort: 0, Label: "Privacy", URL: "/privacy"},
	}
	menus := BuildMenus(items, "", "en", "en", false)
	got := menuLabels(menus["main"])
	want := []string{"Programs", "Programs>EPR", "Programs>Tires", "Resources", "Resources>Links", "About", "Contact"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("main = %v, want %v", got, want)
	}
	if f := menuLabels(menus["footer"]); !reflect.DeepEqual(f, []string{"Privacy"}) {
		t.Errorf("footer = %v", f)
	}
}

// The rules for what is not rendered are unchanged by ordering: children
// of a linked item, grandchildren, label-only children, and (publicly) an
// empty dropdown.
func TestBuildMenusStillDropsOutOfModelRows(t *testing.T) {
	items := []content.MenuItem{
		{ID: 1, Menu: "main", Sort: 0, Label: "About", URL: "/about"},
		{ID: 2, Menu: "main", Sort: 1, Label: "Under a link", URL: "/x", ParentID: menuPtr(1)},
		{ID: 3, Menu: "main", Sort: 2, Label: "More"},
		{ID: 4, Menu: "main", Sort: 3, Label: "Child", URL: "/child", ParentID: menuPtr(3)},
		{ID: 5, Menu: "main", Sort: 4, Label: "Grandchild", URL: "/gc", ParentID: menuPtr(4)},
		{ID: 6, Menu: "main", Sort: 5, Label: "Label-only child", ParentID: menuPtr(3)},
		{ID: 7, Menu: "main", Sort: 6, Label: "Empty"},
		// A child whose parent is not in the menu at all.
		{ID: 8, Menu: "main", Sort: 7, Label: "Orphan", URL: "/orphan", ParentID: menuPtr(99)},
	}
	got := menuLabels(BuildMenus(items, "", "en", "en", false)["main"])
	want := []string{"About", "More", "More>Child"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("public menu = %v, want %v", got, want)
	}
	// Editors keep the empty dropdown so they can fill it; nothing else
	// changes.
	got = menuLabels(BuildMenus(items, "", "en", "en", true)["main"])
	want = []string{"About", "More", "More>Child", "Empty"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("editor menu = %v, want %v", got, want)
	}
}

// MenuOptions.Private shows a private page's item without the rest of the
// editor's view: drafts stay hidden, and so do empty dropdowns.
func TestBuildMenusWithPrivate(t *testing.T) {
	pid := func(id int64) *int64 { return &id }
	slug := func(s string) *string { return &s }
	status := func(s content.Status) *content.Status { return &s }
	private := content.VisibilityPrivate

	items := []content.MenuItem{
		{ID: 1, Menu: "main", Label: "Staff", PageID: pid(1), PageSlug: slug("staff"), PageStatus: status(content.StatusPublished), PageVisibility: &private},
		{ID: 2, Menu: "main", Label: "Draft", PageID: pid(2), PageSlug: slug("draft"), PageStatus: status(content.StatusDraft), PageVisibility: &private},
		{ID: 3, Menu: "main", Label: "HR"},
		{ID: 4, Menu: "main", ParentID: pid(3), Label: "Forms", PageID: pid(4), PageSlug: slug("forms"), PageStatus: status(content.StatusPublished), PageVisibility: &private},
		{ID: 5, Menu: "main", Label: "Empty"},
	}
	labels := func(entries []MenuEntry) []string {
		var out []string
		for _, e := range entries {
			out = append(out, e.Label)
			for _, c := range e.Children {
				out = append(out, e.Label+"/"+c.Label)
			}
		}
		return out
	}

	if got := labels(BuildMenusWith(items, "", "en", "en", MenuOptions{})["main"]); len(got) != 0 {
		t.Errorf("public render shows %v, want nothing", got)
	}
	got := labels(BuildMenusWith(items, "", "en", "en", MenuOptions{Private: true})["main"])
	want := []string{"Staff", "HR", "HR/Forms"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("signed-in render shows %v, want %v", got, want)
	}
	if got := labels(BuildMenusWith(items, "", "en", "en", MenuOptions{Drafts: true})["main"]); len(got) != 5 {
		t.Errorf("editor render shows %v, want all five", got)
	}
}
