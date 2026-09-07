package admin

// The history screens reached through Blog & News. A post is a page
// underneath, so its history is its backing page's and every screen is
// shared with Pages — which is exactly why the post-side wrappers are
// worth their own tests. What differs is the two things they supply: the
// loader that enforces the feed permission, and the URL the screens hang
// off, which carries the post's own id rather than its backing page's.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// vidParams is the {id} and {vid} pair the history screens read.
func vidParams(id, versionID int64) map[string]string {
	return map[string]string{
		"id":  strconv.FormatInt(id, 10),
		"vid": strconv.FormatInt(versionID, 10),
	}
}

// seedPostHistory makes a post with two published editions — "<p>old</p>",
// then "<p>new</p>", which is what the site is serving — and returns them
// newest first, the order the screens show.
func seedPostHistory(t *testing.T, s *server, feed content.Feed, title, slugTail string) (*content.Post, []content.Version) {
	t.Helper()
	ctx := context.Background()
	post := seedPost(t, s, feed, title, slugTail)
	for _, body := range []string{"<p>old</p>", "<p>new</p>"} {
		if err := s.deps.Content.UpsertDraftBlock(ctx, post.ID, "main", "en",
			content.KindHTML, body); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, post.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	versions, err := s.deps.Content.Versions(ctx, post.ID)
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("seeded %d editions, want 2", len(versions))
	}
	return post, versions
}

// The list is the backing page's history, and every link on it has to
// point back through the post's own address — a screen that linked to
// /admin/pages/{pageID} would land the reader on a 404, since a post's
// backing page is not reachable there.
func TestPostVersionsListHangsOffThePostURL(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post, _ := seedPostHistory(t, s, content.FeedBlog, "Historic", "historic")

		rec := httptest.NewRecorder()
		s.postVersions(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/versions",
			nil, idParams(post.PostID)))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		base := "/admin/posts/" + itoa(post.PostID)
		if !strings.Contains(body, base) {
			t.Errorf("the history does not link back through the post's address %q: %q", base, body)
		}
		// The backing page's id is not an address in the Pages section, so
		// it must not appear as one here.
		if pageURL := "/admin/pages/" + itoa(post.ID); strings.Contains(body, pageURL) {
			t.Errorf("the history links to the backing page at %q: %q", pageURL, body)
		}
	})
}

// Restoring overwrites the working copy, so a post holding edits nobody
// has published yet says so before anyone clicks.
func TestPostVersionsListWarnsAboutUnpublishedEdits(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post, _ := seedPostHistory(t, s, content.FeedBlog, "Warned", "warned")

		rec := httptest.NewRecorder()
		s.postVersions(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/versions",
			nil, idParams(post.PostID)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "unpublished draft changes") {
			t.Error("a post with no draft edits warned about them anyway")
		}

		if err := s.deps.Content.UpsertDraftBlock(context.Background(), post.ID, "main", "en",
			content.KindHTML, "<p>an edit nobody has published</p>"); err != nil {
			t.Fatalf("seeding a draft edit: %v", err)
		}

		rec = httptest.NewRecorder()
		s.postVersions(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/versions",
			nil, idParams(post.PostID)))
		if !strings.Contains(rec.Body.String(), "unpublished draft changes") {
			t.Error("a post with draft edits did not warn that a restore would replace them")
		}
	})
}

// The post preview is where the two screens genuinely differ: the post
// template needs the byline and the date, so the stored edition is
// rendered with the post's own data alongside it rather than as a bare
// page.
func TestPostVersionPreviewRendersTheStoredEditionAsAPost(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post, versions := seedPostHistory(t, s, content.FeedBlog, "Previewed", "previewed")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		s.postVersionPreview(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/versions/y/preview",
			nil, vidParams(post.PostID, oldest.ID)))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<p>old</p>") {
			t.Errorf("the preview does not show the stored edition: %q", body)
		}
		// The newest edition is what the site serves; the preview is
		// about the one that is not live.
		if strings.Contains(body, "<p>new</p>") {
			t.Errorf("the preview shows the live edition instead: %q", body)
		}
	})
}

// A version id belonging to another page is not found here, rather than
// another page's content served under this post's address.
func TestPostVersionScreensRefuseAnotherPagesVersion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		mine, _ := seedPostHistory(t, s, content.FeedBlog, "Mine", "mine")
		_, theirs := seedPostHistory(t, s, content.FeedBlog, "Theirs", "theirs")

		for name, h := range map[string]http.HandlerFunc{
			"preview": s.postVersionPreview,
			"restore": s.postVersionRestore,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, url.Values{}, vidParams(mine.PostID, theirs[0].ID)))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
			t.Run(name+" with a non-numeric version id", func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, url.Values{}, map[string]string{
					"id": itoa(mine.PostID), "vid": "latest",
				}))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}

		// Restoring the post's own edition still works, so the refusals
		// above are about the scope and not about the screen being broken.
		rec := httptest.NewRecorder()
		versions, err := s.deps.Content.Versions(context.Background(), mine.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		s.postVersionRestore(rec, formReq(t, s, u, url.Values{}, vidParams(mine.PostID, versions[1].ID)))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("status = %d for the post's own edition, want 303", rec.Code)
		}
	})
}

// A plain restore leaves the site alone: the draft carries the old
// edition, the live site still serves what it was serving, and the
// redirect goes back to the post's form.
func TestPostVersionRestoreStagesWithoutPublishing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post, versions := seedPostHistory(t, s, content.FeedBlog, "Restored", "restored")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{}, vidParams(post.PostID, oldest.ID))
		s.postVersionRestore(rec, r)

		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		if got := draftHTML(t, s, post.ID, "main", "en"); !strings.Contains(got, "<p>old</p>") {
			t.Errorf("draft = %q, want the restored edition", got)
		}
		live, err := s.deps.Content.BlocksFor(context.Background(), post.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("reading published blocks: %v", err)
		}
		var published strings.Builder
		for _, b := range live {
			published.WriteString(b.Content)
		}
		if !strings.Contains(published.String(), "<p>new</p>") {
			t.Errorf("published content = %q, want the site left alone", published.String())
		}
		if f := flashOf(s, r); !strings.Contains(f, "Publish when you're ready") {
			t.Errorf("flash = %q, want the staged-not-live message", f)
		}
	})
}

// "Restore and publish" is the other case — something is wrong on the
// live site now — and does both in one click.
func TestPostVersionRestoreAndPublishGoesLive(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post, versions := seedPostHistory(t, s, content.FeedBlog, "Republished", "republished")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"action": {"publish"}}, vidParams(post.PostID, oldest.ID))
		s.postVersionRestore(rec, r)

		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		live, err := s.deps.Content.BlocksFor(context.Background(), post.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("reading published blocks: %v", err)
		}
		var published strings.Builder
		for _, b := range live {
			published.WriteString(b.Content)
		}
		if !strings.Contains(published.String(), "<p>old</p>") {
			t.Errorf("published content = %q, want the restored edition live", published.String())
		}
		if f := flashOf(s, r); !strings.Contains(f, "live on the site again") {
			t.Errorf("flash = %q, want the published message", f)
		}
	})
}

// The Blog & News routes admit anyone holding either feed, so which posts
// a user may reach is decided by the loader these screens go through. A
// history screen that skipped it would be a way to read — and restore —
// a feed the user does not hold.
func TestPostVersionScreensEnforceTheFeedPermission(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		blogger := formUser(t, s, "history-blogs-only@example.com", auth.RoleEditor, auth.PermBlogs)
		newsPost, versions := seedPostHistory(t, s, content.FeedNews, "News history", "news-history")

		for name, h := range map[string]http.HandlerFunc{
			"list":    s.postVersions,
			"preview": s.postVersionPreview,
			"restore": s.postVersionRestore,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, blogger, url.Values{},
					vidParams(newsPost.PostID, versions[1].ID)))
				if rec.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403", rec.Code)
				}
			})
		}

		// The same screens open for someone who holds news.
		reporter := formUser(t, s, "history-news@example.com", auth.RoleEditor, auth.PermNews)
		rec := httptest.NewRecorder()
		s.postVersions(rec, formReqTo(t, s, reporter, http.MethodGet, "/admin/posts/x/versions",
			nil, idParams(newsPost.PostID)))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d for a news editor, want 200", rec.Code)
		}
	})
}

// A post id nothing answers to is a 404 on every one of the three
// screens, rather than a panic on a nil post.
func TestPostVersionScreensOnAMissingPost(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		for name, h := range map[string]http.HandlerFunc{
			"list":    s.postVersions,
			"preview": s.postVersionPreview,
			"restore": s.postVersionRestore,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, url.Values{}, vidParams(999999, 1)))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}
	})
}
