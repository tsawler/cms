package render

import (
	"io"
	"testing"

	"github.com/tsawler/cms/content"
)

// BenchmarkRender measures one public render of a page with a few
// regions: the cost every page view pays in the renderer.
func BenchmarkRender(b *testing.B) {
	r, err := New(testFS, []string{"base.gohtml"}, []PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil)
	if err != nil {
		b.Fatal(err)
	}
	in := Input{
		Page: &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"},
		Blocks: []content.Block{
			{Region: "site-name", Kind: content.KindText, Content: "Acme"},
			{Region: "tagline", Kind: content.KindText, Content: "We build things"},
			{Region: "main", Kind: content.KindHTML, Content: "<p>Hello</p>"},
		},
		Locale: "en",
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := r.Render(io.Discard, in); err != nil {
			b.Fatal(err)
		}
	}
}
