package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
)

// The picker's API answers a window at a time and says whether there is
// more, so the library never comes back whole.
func TestAPIMediaListPages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		for i := range 3 {
			uploadPNG(t, s, u, fmt.Sprintf("p%d.png", i))
		}
		page := func(query string) map[string]any {
			t.Helper()
			rec := httptest.NewRecorder()
			s.apiMediaList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media?kind=image&"+query, nil, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d (%s)", query, rec.Code, rec.Body.String())
			}
			return decodeAPI(t, rec)
		}

		first := page("limit=2")
		if items := first["media"].([]any); len(items) != 2 || first["total"] != float64(3) || first["more"] != true {
			t.Errorf("first window: %d items, total %v, more %v; want 2, 3, true", len(items), first["total"], first["more"])
		}
		rest := page("limit=2&offset=2")
		if items := rest["media"].([]any); len(items) != 1 || rest["more"] != false {
			t.Errorf("second window: %d items, more %v; want 1, false", len(items), rest["more"])
		}
		// No limit asked for is the default window, not everything.
		if all := page(""); len(all["media"].([]any)) != 3 || all["more"] != false {
			t.Errorf("default window: %v", all)
		}
	})
}

// The media page lists only its tab's kind, a page at a time, while every
// tab's badge counts its own kind.
func TestMediaPageListsOneKindAPageAtATime(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		was := mediaPerPage
		mediaPerPage = 2
		defer func() { mediaPerPage = was }()

		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		for i := range 3 {
			uploadPNG(t, s, u, fmt.Sprintf("pic%d.png", i))
		}
		rec := httptest.NewRecorder()
		s.mediaUpload(rec, uploadReq(t, s, u, "notes.txt", []byte("plain words"), nil))

		get := func(query string) string {
			t.Helper()
			rec := httptest.NewRecorder()
			s.mediaList(rec, formReqTo(t, s, u, http.MethodGet, "/admin/media?"+query, nil, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d", query, rec.Code)
			}
			return rec.Body.String()
		}
		images := get("tab=images")
		if n := strings.Count(images, `class="cms-item" role="option"`); n != 2 {
			t.Errorf("first page of images lists %d items, want 2", n)
		}
		if strings.Contains(images, "notes.txt") {
			t.Error("the images tab listed a document")
		}
		for _, badge := range []string{`Images <span class="cms-tab-count">3</span>`, `Documents <span class="cms-tab-count">1</span>`} {
			if !strings.Contains(images, badge) {
				t.Errorf("tab badge %q missing", badge)
			}
		}
		if !strings.Contains(images, "/admin/media?dir=asc&amp;page=2&amp;sort=name&amp;tab=images") {
			t.Errorf("no link to the second page of the images tab")
		}
		if n := strings.Count(get("tab=images&page=2"), `class="cms-item" role="option"`); n != 1 {
			t.Errorf("second page of images lists %d items, want 1", n)
		}
	})
}

// A bulk action takes at most maxBulk items: more is refused with a
// message, and nothing is done.
func TestBulkActionsRefuseAnOversizedSelection(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "kept.png")
		folder, err := s.deps.Media.CreateFolder(t.Context(), "Elsewhere", media.KindImage)
		if err != nil {
			t.Fatal(err)
		}

		form := url.Values{"folder": {strconv.FormatInt(folder.ID, 10)}, "id": {strconv.FormatInt(md.ID, 10)}}
		for i := range maxBulk {
			form.Add("id", strconv.Itoa(1_000_000+i))
		}
		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodPost, "/admin/media/bulk/move", form, nil)
		s.mediaBulkMove(rec, r)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want a redirect back to the library", rec.Code)
		}
		if got, _ := s.deps.Media.GetByID(t.Context(), md.ID, "en"); got.FolderID != nil {
			t.Error("an oversized selection was acted on")
		}
		if msg := s.deps.Sessions.PopString(r.Context(), sessionKeyFlash); !strings.Contains(msg, "at most") {
			t.Errorf("flash = %q, want the reason", msg)
		}
	})
}

// The media page is ordered by the server across all its pages, so the
// first page by name holds the first names — not the first names among
// the newest uploads. The order comes from the URL, else the remembered
// choice, else by name; page links carry it.
func TestMediaPageIsOrderedAcrossPages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		was := mediaPerPage
		mediaPerPage = 2
		defer func() { mediaPerPage = was }()

		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		// Newest first these are d c b a; by name, a b c d.
		for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
			uploadPNG(t, s, u, name)
		}
		names := func(query string, cookie *http.Cookie) (string, string) {
			t.Helper()
			r := formReqTo(t, s, u, http.MethodGet, "/admin/media?"+query, nil, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			s.mediaList(rec, r)
			body := rec.Body.String()
			var out []string
			for _, part := range strings.Split(body, `data-name="`)[1:] {
				out = append(out, strings.TrimSuffix(part[:strings.Index(part, `"`)], ".png"))
			}
			return strings.Join(out, " "), body
		}

		if got, body := names("tab=images", nil); got != "a b" {
			t.Errorf("first page by default = %q, want the first two names, a b", got)
		} else if !strings.Contains(body, "page=2&amp;sort=name&amp;tab=images") {
			t.Error("the page links do not carry the order")
		}
		if got, _ := names("tab=images&page=2", nil); got != "c d" {
			t.Errorf("second page by default = %q, want c d", got)
		}
		if got, _ := names("tab=images&sort=name&dir=desc", nil); got != "d c" {
			t.Errorf("first page Z to A = %q, want d c", got)
		}
		remembered := &http.Cookie{Name: mediaSortCookie, Value: "name:desc"}
		if got, _ := names("tab=images", remembered); got != "d c" {
			t.Errorf("first page with the order remembered = %q, want d c", got)
		}
		if got, _ := names("tab=images&sort=name&dir=asc", remembered); got != "a b" {
			t.Errorf("the URL's order should beat the remembered one, got %q", got)
		}
		if got, _ := names("tab=images&sort=bogus", nil); got != "a b" {
			t.Errorf("an unknown column = %q, want by name", got)
		}
	})
}
