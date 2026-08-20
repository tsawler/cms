package render

import (
	"path"
	"sort"
	"text/template/parse"
)

// Region is an editable area a page template declares via {{cmsText "key"}},
// {{cmsRegion "key"}}, {{cmsImage "key"}}, or {{cmsSections "key"}}.
type Region struct {
	Name string
	Kind string // "text", "html", "image", "sections", or "shared"
}

// KindShared is the Kind of a region declared with {{cmsShared "key"}}:
// content stored once for the whole site rather than per page. Regions
// never returns one — a shared region is not the page's to save — and
// SharedRegions returns nothing else.
const KindShared = "shared"

// Regions walks the parse tree of a page template (and every template it
// invokes) and returns the editable regions it declares, in source order,
// deduplicated. This is how the admin UI knows which content fields to show
// for a page without the developer maintaining a separate region list.
//
// Shared regions are left out: they belong to the site, not to any page
// that happens to render them (see SharedRegions).
func (r *Renderer) Regions(templateFile string) []Region {
	out := r.declaredRegions(templateFile)
	kept := out[:0]
	for _, region := range out {
		if region.Kind != KindShared {
			kept = append(kept, region)
		}
	}
	return kept
}

// SharedRegions returns every shared region ({{cmsShared "key"}}) any of
// the host's templates declares, deduplicated, plus the notice bar's
// own (NoticeRegion). Shared content has no template of its own to be
// validated against — it is reached from whichever page an editor
// happens to be on — so this union is what a save checks a region name
// against.
func (r *Renderer) SharedRegions() []Region {
	// The notice bar's words are a shared region the CMS owns rather
	// than one a template declares: {{cmsNotice}} need never appear in
	// any template — the bar is injected for layouts that don't place it
	// — so nothing else here would vouch for the region and a save of it
	// would be dropped on the floor. Listing it unconditionally costs
	// nothing while the bar is off: a region nobody writes has no rows.
	out := []Region{{Name: NoticeRegion, Kind: KindShared}}
	seen := map[string]bool{NoticeRegion: true}
	// Sorted, so the list does not shuffle with map iteration order.
	files := make([]string, 0, len(r.sets))
	for file := range r.sets {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		for _, region := range r.declaredRegions(file) {
			if region.Kind == KindShared && !seen[region.Name] {
				seen[region.Name] = true
				out = append(out, region)
			}
		}
	}
	return out
}

// declaredRegions is Regions without the shared filter — every region the
// template set declares, in source order.
func (r *Renderer) declaredRegions(templateFile string) []Region {
	set, ok := r.sets[templateFile]
	if !ok {
		return nil
	}

	var out []Region
	seenRegion := map[string]bool{}
	visited := map[string]bool{}

	var visit func(name string)
	var walk func(node parse.Node)

	record := func(kind, name string) {
		if !seenRegion[name] {
			seenRegion[name] = true
			out = append(out, Region{Name: name, Kind: kind})
		}
	}

	var inspectPipe func(p *parse.PipeNode)
	inspectPipe = func(p *parse.PipeNode) {
		if p == nil {
			return
		}
		for _, cmd := range p.Cmds {
			if len(cmd.Args) >= 2 {
				if ident, ok := cmd.Args[0].(*parse.IdentifierNode); ok {
					if str, ok := cmd.Args[1].(*parse.StringNode); ok {
						switch ident.Ident {
						case "cmsText":
							record("text", str.Text)
						case "cmsRegion":
							record("html", str.Text)
						case "cmsImage":
							record("image", str.Text)
						case "cmsSections":
							record("sections", str.Text)
						case "cmsShared":
							record(KindShared, str.Text)
						}
					}
				}
			}
			for _, arg := range cmd.Args {
				if nested, ok := arg.(*parse.PipeNode); ok {
					inspectPipe(nested)
				}
			}
		}
	}

	walk = func(node parse.Node) {
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, item := range n.Nodes {
				walk(item)
			}
		case *parse.ActionNode:
			inspectPipe(n.Pipe)
		case *parse.IfNode:
			inspectPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			inspectPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			inspectPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.TemplateNode:
			inspectPipe(n.Pipe)
			visit(n.Name)
		}
	}

	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		t := set.Lookup(name)
		if t == nil || t.Tree == nil || t.Tree.Root == nil {
			return
		}
		walk(t.Tree.Root)
	}

	visit(path.Base(templateFile))
	return out
}
