package admin

// The snippet library's write handlers. A snippet is a block of markup
// the editor's palette offers, and a "section preset" carries the section
// settings that come with it — so what these pin down is that a
// submission becomes a usable palette entry, and that its settings are
// resolved the same way the editor's own ⚙ dialog resolves them.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/snippets"
)

// snippetServer is formServer with the snippet store wired, which is what
// leaves these handlers with somewhere to write.
func snippetServer(t *testing.T, db *sqldb.DB) *server {
	t.Helper()
	s := formServer(t, db)
	s.deps.Snippets = snippets.NewStore(db)
	return s
}

func snippetForm() url.Values {
	return url.Values{
		"name": {"Call to action"},
		"html": {`<p class="cms-snippet">Buy now</p>`},
		"kind": {"block"},
	}
}

// storedSnippet finds a snippet by name, which is how these tests check
// what the handler wrote rather than what it was handed.
func storedSnippet(t *testing.T, s *server, name string) *snippets.Snippet {
	t.Helper()
	all, err := s.deps.Snippets.All(context.Background())
	if err != nil {
		t.Fatalf("listing snippets: %v", err)
	}
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}

func TestSnippetCreate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, snippetForm(), nil)
		s.snippetCreate(rec, r)

		wantRedirect(t, rec, "/admin/snippets")
		sn := storedSnippet(t, s, "Call to action")
		if sn == nil {
			t.Fatal("the snippet was not stored")
		}
		if !strings.Contains(sn.HTML, "Buy now") {
			t.Errorf("html = %q, want the submitted markup", sn.HTML)
		}
		// A plain block carries no settings: an empty map would read as a
		// section preset downstream.
		if len(sn.Settings) != 0 {
			t.Errorf("settings = %v, want none on a plain block", sn.Settings)
		}
		if f := flashOf(s, r); !strings.Contains(f, "Snippet created") {
			t.Errorf("flash = %q, want the created message", f)
		}
	})
}

// A section preset always stores a background and a width, so the map is
// never empty; the optional axes store only a non-default choice, and
// anything unrecognized resolves to the default rather than being kept.
func TestSnippetCreateResolvesPresetSettings(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		form := snippetForm()
		form.Set("name", "Dark banner")
		form.Set("kind", "preset")
		form.Set("set_bg", "dark")
		form.Set("set_width", "full")
		form.Set("set_corners", "large")
		form.Set("set_height", "50")
		form.Set("set_valign", "center")
		form.Set("set_padding", "nonsense")
		form.Set("set_size", "nonsense")

		rec := httptest.NewRecorder()
		s.snippetCreate(rec, formReq(t, s, u, form, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}

		sn := storedSnippet(t, s, "Dark banner")
		if sn == nil {
			t.Fatal("the snippet was not stored")
		}
		if sn.Settings["bg"] != "dark" || sn.Settings["width"] != "full" {
			t.Errorf("settings = %v, want bg=dark width=full", sn.Settings)
		}
		if sn.Settings["corners"] != "large" {
			t.Errorf("corners = %q, want the non-default choice stored", sn.Settings["corners"])
		}
		if sn.Settings["height"] != "50" || sn.Settings["valign"] != "center" {
			t.Errorf("settings = %v, want the submitted height and alignment", sn.Settings)
		}
		// An unrecognized value on an optional axis resolves to the
		// default, and a default is not stored.
		for _, key := range []string{"padding", "size"} {
			if v, ok := sn.Settings[key]; ok {
				t.Errorf("%s = %q, want a resolved default left unstored", key, v)
			}
		}
	})
}

func TestSnippetCreateRejectsAnEmptyForm(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		form := snippetForm()
		form.Set("name", "  ")
		form.Set("html", "  ")

		rec := httptest.NewRecorder()
		s.snippetCreate(rec, formReq(t, s, u, form, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Name is required") {
			t.Errorf("the form does not mark the missing name: %q", body)
		}
		if !strings.Contains(body, "needs some HTML") {
			t.Errorf("the form does not mark the missing markup: %q", body)
		}
		all, err := s.deps.Snippets.All(context.Background())
		if err != nil {
			t.Fatalf("listing snippets: %v", err)
		}
		if len(all) != 0 {
			t.Errorf("%d snippets stored, want a rejected form to store nothing", len(all))
		}
	})
}

func TestSnippetUpdateAndDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.snippetCreate(rec, formReq(t, s, u, snippetForm(), nil))
		sn := storedSnippet(t, s, "Call to action")
		if sn == nil {
			t.Fatal("the snippet was not stored")
		}

		form := snippetForm()
		form.Set("name", "Call to action v2")
		form.Set("html", `<p class="cms-snippet">Buy later</p>`)
		rec = httptest.NewRecorder()
		r := formReq(t, s, u, form, idParams(sn.ID))
		s.snippetUpdate(rec, r)

		wantRedirect(t, rec, "/admin/snippets")
		after, err := s.deps.Snippets.GetByID(context.Background(), sn.ID)
		if err != nil {
			t.Fatalf("reloading: %v", err)
		}
		if after.Name != "Call to action v2" || !strings.Contains(after.HTML, "Buy later") {
			t.Errorf("snippet = %+v, want the submitted values", after)
		}
		if f := flashOf(s, r); !strings.Contains(f, "Snippet saved") {
			t.Errorf("flash = %q, want the saved message", f)
		}

		// A rejected edit re-renders the form and changes nothing.
		bad := snippetForm()
		bad.Set("name", "")
		rec = httptest.NewRecorder()
		s.snippetUpdate(rec, formReq(t, s, u, bad, idParams(sn.ID)))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", rec.Code)
		}
		if again, err := s.deps.Snippets.GetByID(context.Background(), sn.ID); err != nil {
			t.Fatalf("reloading: %v", err)
		} else if again.Name != "Call to action v2" {
			t.Errorf("name = %q, want the rejected edit to have changed nothing", again.Name)
		}

		rec = httptest.NewRecorder()
		r = formReq(t, s, u, nil, idParams(sn.ID))
		s.snippetDelete(rec, r)
		wantRedirect(t, rec, "/admin/snippets")
		if _, err := s.deps.Snippets.GetByID(context.Background(), sn.ID); err == nil {
			t.Error("the snippet is still there")
		}
		// Deleting a palette entry does not reach into pages that already
		// used it, and the message says so.
		if f := flashOf(s, r); !strings.Contains(f, "already inserted") {
			t.Errorf("flash = %q, want the message about existing copies", f)
		}
	})
}

func TestSnippetFormsOnMissingIDs(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		for name, h := range map[string]http.HandlerFunc{
			"edit":   s.snippetEdit,
			"update": s.snippetUpdate,
			"delete": s.snippetDelete,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, snippetForm(), idParams(999999)))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
			t.Run(name+" with a non-numeric id", func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, snippetForm(), map[string]string{"id": "abc"}))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}
	})
}

func TestSnippetNewAndEditRenderTheForm(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := snippetServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.snippetNew(rec, formReqTo(t, s, u, http.MethodGet, "/admin/snippets/new", nil, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `name="html"`) {
			t.Error("the new-snippet form is missing its markup field")
		}

		rec = httptest.NewRecorder()
		s.snippetCreate(rec, formReq(t, s, u, snippetForm(), nil))
		sn := storedSnippet(t, s, "Call to action")
		if sn == nil {
			t.Fatal("the snippet was not stored")
		}

		rec = httptest.NewRecorder()
		s.snippetEdit(rec, formReqTo(t, s, u, http.MethodGet, "/admin/snippets/x", nil, idParams(sn.ID)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Buy now") {
			t.Errorf("the edit form does not show the stored markup: %q", rec.Body.String())
		}
	})
}
