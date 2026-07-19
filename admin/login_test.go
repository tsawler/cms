package admin

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
)

// newLoginTestServer serves the admin handler with in-memory sessions and no
// database. That is enough for the login paths that reject a request before
// credentials are checked (throttle, honeypot, CAPTCHA).
func newLoginTestServer(t *testing.T, cap *captcha.Client) (*httptest.Server, *http.Client) {
	t.Helper()
	h := New(Deps{
		Sessions:  scs.New(),
		Users:     auth.NewStore(nil),
		Captcha:   cap,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, &http.Client{Jar: jar}
}

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// getLogin fetches the login form and returns the CSRF token bound to the
// client's session, plus the page body.
func getLogin(t *testing.T, srv *httptest.Server, client *http.Client) (string, string) {
	t.Helper()
	resp, err := client.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := csrfRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token in login page:\n%s", body)
	}
	return string(m[1]), string(body)
}

func postLogin(t *testing.T, srv *httptest.Server, client *http.Client, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

// The login form must offer "Remember me", labelled with the configured
// duration in hours.
func TestLoginRememberCheckbox(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	_, page := getLogin(t, srv, client)
	if !strings.Contains(page, `name="remember"`) {
		t.Errorf("login page missing remember checkbox:\n%s", page)
	}
	if !strings.Contains(page, "Remember me for 24 hours") {
		t.Errorf("login page missing default 24h label:\n%s", page)
	}
}

// Without CAPTCHA configured, the config script must 404.
func TestCaptchaConfigJSDisabled(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	resp, err := client.Get(srv.URL + "/captcha.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("captcha.js without captcha: status = %d, want 404", resp.StatusCode)
	}
}

// A filled honeypot must be rejected with the same message as bad
// credentials, and must count toward the login throttle.
func TestLoginHoneypot(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	csrfToken, _ := getLogin(t, srv, client)

	form := url.Values{
		"csrf_token": {csrfToken},
		"email":      {"bot@example.com"},
		"password":   {"hunter2"},
		"website":    {"https://spam.example.com"},
	}
	resp, body := postLogin(t, srv, client, form)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(body, "didn&#39;t work") {
		t.Errorf("honeypot rejection should look like a credential failure:\n%s", body)
	}

	// Five strikes exhaust the throttle for this email+IP.
	for range 4 {
		postLogin(t, srv, client, form)
	}
	resp, _ = postLogin(t, srv, client, form)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 5 honeypot hits: status = %d, want 429", resp.StatusCode)
	}
}

// With CAPTCHA configured, the login page embeds the widget and a POST
// without a token is rejected before credentials are checked.
func TestLoginCaptchaRequired(t *testing.T) {
	cap, err := captcha.New(captcha.Config{URL: "http://cap.invalid:3000", SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)

	csrfToken, page := getLogin(t, srv, client)
	if !strings.Contains(page, `<cap-widget data-cap-api-endpoint="http://cap.invalid:3000/site1/">`) {
		t.Errorf("login page missing cap widget:\n%s", page)
	}
	if !strings.Contains(page, `src="http://cap.invalid:3000/assets/widget.js"`) {
		t.Errorf("login page missing widget script:\n%s", page)
	}
	if !strings.Contains(page, `src="/admin/captcha.js"`) {
		t.Errorf("login page missing captcha config script:\n%s", page)
	}

	resp0, err := client.Get(srv.URL + "/captcha.js")
	if err != nil {
		t.Fatal(err)
	}
	js, _ := io.ReadAll(resp0.Body)
	resp0.Body.Close()
	if want := `window.CAP_CUSTOM_WASM_URL = "http://cap.invalid:3000/assets/cap_wasm_bg.wasm";`; !strings.Contains(string(js), want) {
		t.Errorf("captcha.js = %q, want it to contain %q", js, want)
	}

	resp, body := postLogin(t, srv, client, url.Values{
		"csrf_token": {csrfToken},
		"email":      {"user@example.com"},
		"password":   {"pw"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(body, "complete the verification") {
		t.Errorf("expected missing-captcha message:\n%s", body)
	}
}

// A token the Cap server rejects must fail the login.
func TestLoginCaptchaRejected(t *testing.T) {
	capSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success": false}`))
	}))
	defer capSrv.Close()

	cap, err := captcha.New(captcha.Config{URL: capSrv.URL, SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)
	csrfToken, _ := getLogin(t, srv, client)

	resp, body := postLogin(t, srv, client, url.Values{
		"csrf_token": {csrfToken},
		"email":      {"user@example.com"},
		"password":   {"pw"},
		"cap-token":  {"forged"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(body, "Verification failed") {
		t.Errorf("expected captcha-rejected message:\n%s", body)
	}
}

// The CSP must admit the Cap origin only when CAPTCHA is configured.
func TestSecureHeadersCaptchaCSP(t *testing.T) {
	cap, err := captcha.New(captcha.Config{URL: "https://cap.example.com", SiteKey: "k", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)
	resp, err := client.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"script-src 'self' 'wasm-unsafe-eval' https://cap.example.com",
		"style-src 'self' 'unsafe-inline'",
		"connect-src 'self' https://cap.example.com",
		"worker-src 'self' blob:",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}

	srvOff, clientOff := newLoginTestServer(t, nil)
	resp, err = clientOff.Get(srvOff.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "cap.example.com") {
		t.Errorf("CSP mentions cap server without captcha configured: %s", csp)
	}
}
