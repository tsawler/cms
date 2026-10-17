package render

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"sync"
	"testing"

	"github.com/tsawler/cms/content"
)

// Renders reuse template copies (see acquire), so nothing one render
// binds may show up in the next: not an edit render's markers in a public
// page, not one request's host function in another request.
func TestReusedTemplatesCarryNothingOver(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	render := func(in Input) string {
		t.Helper()
		in.Page, in.Locale = page, "en"
		var buf bytes.Buffer
		if err := r.Render(&buf, in); err != nil {
			t.Fatalf("Render: %v", err)
		}
		return buf.String()
	}

	render(Input{Edit: &EditInfo{PageID: 1, AdminPath: "/admin", Locale: "en"}})
	if out := render(Input{}); strings.Contains(out, "data-cms-region") {
		t.Errorf("a public render after an edit render carries edit markers:\n%s", out)
	}

	host := hostFuncRenderer(t, template.FuncMap{
		"vehicleCount":     func() int { return 7 },
		"featuredVehicles": func(int) []testVehicle { return nil },
	})
	mine := renderHome(t, host, Input{Funcs: template.FuncMap{"vehicleCount": func() int { return 99 }}})
	if !strings.Contains(mine, "99 in stock") {
		t.Fatalf("the request's own function was not used:\n%s", mine)
	}
	if next := renderHome(t, host, Input{}); !strings.Contains(next, "7 in stock") {
		t.Errorf("a render without its own function got the previous request's:\n%s", next)
	}
}

// Concurrent renders of one template set each get a copy to themselves:
// every page comes out with its own content and none of another's. Run
// with -race, this is also the test that the pool hands no copy to two
// renders at once.
func TestConcurrentRendersStayApart(t *testing.T) {
	r := newTestRenderer(t)
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf("content-%d-end", i)
			in := Input{
				Page:   &content.Page{ID: int64(i), TemplateName: "pages/home.gohtml", Title: "Home"},
				Blocks: []content.Block{{Region: "main", Kind: content.KindHTML, Content: "<p>" + want + "</p>"}},
				Locale: "en",
			}
			if i%3 == 0 {
				in.Edit = &EditInfo{PageID: int64(i), AdminPath: "/admin", Locale: "en"}
			}
			var buf bytes.Buffer
			if err := r.Render(&buf, in); err != nil {
				errs <- err.Error()
				return
			}
			out := buf.String()
			if !strings.Contains(out, want) || strings.Count(out, "content-") != 1 {
				errs <- fmt.Sprintf("render %d got someone else's content:\n%s", i, out)
			}
			if (in.Edit == nil) == strings.Contains(out, "data-cms-region") {
				errs <- fmt.Sprintf("render %d: edit markers %v on an edit=%v render", i, strings.Contains(out, "data-cms-region"), in.Edit != nil)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
