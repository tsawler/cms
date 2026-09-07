package admin

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// renderServer is a server with just enough wired for the rendering path:
// the templates it looks pages up in, and the logger both it and
// serverError write to.
func renderServer(t *testing.T) *server {
	t.Helper()
	return &server{
		templates: parseTemplates(),
		deps: Deps{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}
}

func TestRenderWritesTheRequestedStatus(t *testing.T) {
	s := renderServer(t)
	rec := httptest.NewRecorder()

	// 404 rather than 200: a "no such page" screen that answers OK is one
	// a crawler will index.
	s.render(rec, http.StatusNotFound, "dashboard", templateData{
		AdminPath: "/admin",
		AdminLang: "en",
	})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("nothing was rendered")
	}
}

// A page name that is not in the template set is a programming mistake,
// not a user one — it has to become a 500 rather than a blank 200, which
// is what a silent return would leave behind.
func TestRenderUnknownTemplateIsAServerError(t *testing.T) {
	s := renderServer(t)
	rec := httptest.NewRecorder()

	s.render(rec, http.StatusOK, "no_such_template", templateData{})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "no_such_template") {
		t.Errorf("the response names the missing template: %q", rec.Body.String())
	}
}

// serverError is the one place an unexpected failure reaches the browser,
// so what it says is deliberately not what went wrong: the detail goes to
// the log, and the reader gets something they can act on.
func TestServerError(t *testing.T) {
	var logged strings.Builder
	s := &server{deps: Deps{
		Logger: slog.New(slog.NewTextHandler(&logged, nil)),
	}}
	rec := httptest.NewRecorder()

	// The kind of thing a store hands back when the database goes away:
	// useful in a log, meaningless and alarming on screen.
	err := errors.New("dial tcp 127.0.0.1:5432: connection refused")
	s.serverError(rec, err)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	body := rec.Body.String()
	if strings.Contains(body, err.Error()) {
		t.Errorf("the response leaks the underlying error: %q", body)
	}
	if !strings.Contains(body, "Something went wrong") {
		t.Errorf("body = %q, want the generic apology", body)
	}
	if !strings.Contains(logged.String(), err.Error()) {
		t.Errorf("the error was not logged: %q", logged.String())
	}
}
