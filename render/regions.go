package render

import (
	"path"
	"text/template/parse"
)

// Region is an editable area a page template declares via {{cmsText "key"}},
// {{cmsRegion "key"}}, {{cmsImage "key"}}, or {{cmsSections "key"}}.
type Region struct {
	Name string
	Kind string // "text", "html", "image", or "sections"
}

// Regions walks the parse tree of a page template (and every template it
// invokes) and returns the editable regions it declares, in source order,
// deduplicated. This is how the admin UI knows which content fields to show
// for a page without the developer maintaining a separate region list.
func (r *Renderer) Regions(templateFile string) []Region {
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
