package admin

// The request body ceiling, which lives in the CSRF middleware because
// that is the first thing in the chain to read a body.
//
// The bug these pin: PostFormValue on a multipart request calls
// ParseMultipartForm, which streams the whole body to memory and temp
// files with no total cap — and then returns nil on every later call,
// because it short-circuits once r.MultipartForm is set. Handlers that
// set http.MaxBytesReader and re-parse with a smaller bound were writing
// dead code that read as though it worked. Worst case was unauthenticated:
// POST /admin/login is an unsafe method, so a multipart body of any size
// was buffered to disk before the credentials were ever looked at.

import (
	"bytes"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
)

// multipartBody builds a body with csrf_token first and a file of
// fileSize bytes after it, the shape every upload form posts.
func multipartBody(t *testing.T, token string, fileSize int) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("csrf_token", token); err != nil {
		t.Fatal(err)
	}
	fw, err := w.CreateFormFile("upload", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("A"), fileSize)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// bodyLimitServer is a server with just enough wired for the middleware:
// a session manager and a logger. deps.MaxRequestBytes is the knob under
// test; signedIn decides which ceiling applies.
func bodyLimitServer(maxBytes int64) *server {
	return &server{deps: Deps{
		Sessions:        scs.New(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath:       "/admin",
		MaxRequestBytes: maxBytes,
	}}
}

// runCSRF drives the middleware through the session manager, so
// EnsureCSRF and the session lookup behave as they do in production.
// signedIn plants a user id, which is what maxRequestBytes reads.
func runCSRF(t *testing.T, s *server, req *http.Request, signedIn bool) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var handler http.Handler = s.csrf(inner)
	if signedIn {
		outer := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.deps.Sessions.Put(r.Context(), sessionKeyUserID, int64(1))
			outer.ServeHTTP(w, r)
		})
	}

	rec := httptest.NewRecorder()
	s.deps.Sessions.LoadAndSave(handler).ServeHTTP(rec, req)
	return rec
}

// A signed-out caller posting a large multipart body to the login form is
// the case that needed a ceiling most: no account, no session, and the
// body was buffered to disk before the password was even read.
func TestAnonymousMultipartBodyIsCapped(t *testing.T) {
	s := bodyLimitServer(0)
	body, ct := multipartBody(t, "irrelevant", 4<<20) // 4MB, over the 1MB anon cap
	req := httptest.NewRequest("POST", "/admin/login", body)
	req.Header.Set("Content-Type", ct)

	rec := runCSRF(t, s, req, false)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 — an anonymous 4MB multipart body was accepted", rec.Code)
	}
}

// The ordinary signed-out post — a login form's few short fields — must
// still go through, or the cap has broken the login page.
func TestAnonymousSmallFormIsNotCapped(t *testing.T) {
	s := bodyLimitServer(0)
	form := strings.NewReader("csrf_token=wrong&email=a@example.com&password=hunter2")
	req := httptest.NewRequest("POST", "/admin/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := runCSRF(t, s, req, false)
	// The token is wrong, so 403 — the point is that it reached the token
	// comparison at all rather than being refused as oversized.
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Error("a normal login-sized form was refused as too large")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 from the token comparison", rec.Code)
	}
}

// A signed-in user gets the configured ceiling, not the anonymous one, so
// a large legitimate upload is not caught by the tighter bound.
func TestSignedInGetsTheConfiguredCeiling(t *testing.T) {
	s := bodyLimitServer(8 << 20) // 8MB
	body, ct := multipartBody(t, "irrelevant", 4<<20)
	req := httptest.NewRequest("POST", "/admin/media/upload", body)
	req.Header.Set("Content-Type", ct)

	rec := runCSRF(t, s, req, true)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Error("a 4MB upload was refused under an 8MB ceiling")
	}

	// And the ceiling still bites above itself.
	big, ct2 := multipartBody(t, "irrelevant", 12<<20)
	req2 := httptest.NewRequest("POST", "/admin/media/upload", big)
	req2.Header.Set("Content-Type", ct2)
	if rec2 := runCSRF(t, s, req2, true); rec2.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 — a 12MB body passed an 8MB ceiling", rec2.Code)
	}
}

// The header path leaves the body untouched, which is what lets a
// handler's own MaxBytesReader still govern it. The JS uploaders post
// this way, and were never affected by the bug.
func TestHeaderTokenLeavesTheBodyUnread(t *testing.T) {
	s := bodyLimitServer(0)
	sess := scs.New()
	s.deps.Sessions = sess

	var token string
	var gotBody bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the middleware had parsed, MultipartForm would be set.
		gotBody = r.MultipartForm == nil
		w.WriteHeader(http.StatusOK)
	})

	// Mint a token in a first request, then echo it back in the header.
	// The GET has to go through the middleware itself: EnsureCSRF is what
	// creates the token, and reading the session without it yields "".
	mint := httptest.NewRecorder()
	sess.LoadAndSave(s.csrf(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token, _ = sess.Get(r.Context(), sessionKeyCSRF).(string)
	}))).ServeHTTP(mint, httptest.NewRequest("GET", "/admin/", nil))
	if token == "" {
		t.Fatal("no CSRF token was minted by the GET")
	}

	body, ct := multipartBody(t, "unused", 2<<20)
	req := httptest.NewRequest("POST", "/admin/media/upload", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-CSRF-Token", token)
	for _, c := range mint.Result().Cookies() {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	sess.LoadAndSave(s.csrf(inner)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the header token was not accepted", rec.Code)
	}
	if !gotBody {
		t.Error("the middleware parsed the body even though the header carried the token")
	}
}

// The ceiling has to be larger than any one upload the admin accepts,
// because a multipart post carries every field at once. This pins the
// relationship rather than the number.
func TestDefaultCeilingExceedsASingleUpload(t *testing.T) {
	if DefaultMaxRequestBytes <= maxAnonRequestBytes {
		t.Errorf("the signed-in ceiling (%d) is not above the anonymous one (%d)",
			DefaultMaxRequestBytes, maxAnonRequestBytes)
	}
	if csrfParseMemory > DefaultMaxRequestBytes {
		t.Errorf("parse memory (%d) exceeds the default ceiling (%d), so the ceiling never binds",
			csrfParseMemory, DefaultMaxRequestBytes)
	}
}
