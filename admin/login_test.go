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
	_, token, body := getLoginResponse(t, srv, client)
	return token, body
}

// getLoginResponse is getLogin plus the response itself, for the tests that
// have to compare the page against the headers it was served with.
func getLoginResponse(t *testing.T, srv *httptest.Server, client *http.Client) (*http.Response, string, string) {
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
	return resp, string(m[1]), string(body)
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
// duration (days when it is a whole number of days, hours otherwise).
func TestLoginRememberCheckbox(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	_, page := getLogin(t, srv, client)
	if !strings.Contains(page, `name="remember"`) {
		t.Errorf("login page missing remember checkbox:\n%s", page)
	}
	if !strings.Contains(page, "Remember me for 30 days") {
		t.Errorf("login page missing default 30-day label:\n%s", page)
	}
}

// Without CAPTCHA configured, the login page carries no widget config and
// the CSP needs no nonce.
func TestCaptchaConfigAbsentWithoutCaptcha(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	resp, _, page := getLoginResponse(t, srv, client)
	for _, unwanted := range []string{"CAP_CUSTOM_WASM_URL", "CAP_PAKO_URL", "CAP_SCRIPT_NONCE"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("login page sets %s without captcha configured:\n%s", unwanted, page)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "nonce-") {
		t.Errorf("CSP carries a nonce without captcha configured: %s", csp)
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

// With CAPTCHA configured (invisible by default), the login page wires up
// the programmatic solver and a POST without a token is rejected before
// credentials are checked.
func TestLoginCaptchaRequired(t *testing.T) {
	cap, err := captcha.New(captcha.Config{URL: "http://cap.invalid:3000", SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)

	resp1, csrfToken, page := getLoginResponse(t, srv, client)
	if strings.Contains(page, "<cap-widget") {
		t.Errorf("invisible mode should not render the cap widget:\n%s", page)
	}
	if !strings.Contains(page, `data-cap-endpoint="http://cap.invalid:3000/site1/"`) {
		t.Errorf("login form missing data-cap-endpoint:\n%s", page)
	}
	if !strings.Contains(page, `<input type="hidden" name="cap-token" value="">`) {
		t.Errorf("login form missing hidden cap-token field:\n%s", page)
	}
	if !strings.Contains(page, `src="/admin/static/login-captcha.js"`) {
		t.Errorf("login page missing invisible solver script:\n%s", page)
	}
	if !strings.Contains(page, `src="http://cap.invalid:3000/assets/widget.js"`) {
		t.Errorf("login page missing widget script:\n%s", page)
	}
	// The widget's two CDN dependencies are redirected at copies the CSP
	// admits, and the nonce is handed over for its instrumentation frame.
	for _, want := range []string{
		`window.CAP_CUSTOM_WASM_URL = "http://cap.invalid:3000/assets/cap_wasm_bg.wasm";`,
		`window.CAP_PAKO_URL = "/admin/static/pako_inflate.min.js";`,
		`window.CAP_SCRIPT_NONCE = "`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("login page missing %q:\n%s", want, page)
		}
	}

	// The nonce in the config script must be the one this response's CSP
	// actually allows, or the browser blocks it.
	nonce := regexp.MustCompile(`<script nonce="([^"]+)">`).FindStringSubmatch(page)
	if nonce == nil {
		t.Fatalf("login page has no nonced config script:\n%s", page)
	}
	if csp := resp1.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-"+nonce[1]+"'") {
		t.Errorf("CSP %q does not admit the page's nonce %q", csp, nonce[1])
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

// With Visible set, the login page embeds the checkbox widget instead of
// the invisible solver.
func TestLoginCaptchaVisibleWidget(t *testing.T) {
	cap, err := captcha.New(captcha.Config{URL: "http://cap.invalid:3000", SiteKey: "site1", Secret: "s", Visible: true})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)

	_, page := getLogin(t, srv, client)
	if !strings.Contains(page, `<cap-widget data-cap-api-endpoint="http://cap.invalid:3000/site1/">`) {
		t.Errorf("login page missing cap widget:\n%s", page)
	}
	if strings.Contains(page, "data-cap-endpoint=") {
		t.Errorf("visible mode should not mark the form for invisible solving:\n%s", page)
	}
	if strings.Contains(page, "login-captcha.js") {
		t.Errorf("visible mode should not load the invisible solver script:\n%s", page)
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
		"'wasm-unsafe-eval' 'unsafe-eval' https://cap.example.com",
		"'nonce-",
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

// The CAPTCHA's concessions belong to the login page alone. Every other
// admin page must get the same strict policy a deployment without CAPTCHA
// serves — this is what keeps 'unsafe-eval' off the pages that actually
// render stored content.
func TestSecureHeadersCaptchaCSPIsLoginOnly(t *testing.T) {
	cap, err := captcha.New(captcha.Config{URL: "https://cap.example.com", SiteKey: "k", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	srv, client := newLoginTestServer(t, cap)

	// Any admin path other than the login form. Unauthenticated it
	// redirects to login, so the redirect must not be followed — otherwise
	// this would measure the login page's policy and pass vacuously.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / = %d, want a redirect to login (the un-followed response)", resp.StatusCode)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, unwanted := range []string{"'unsafe-eval'", "'nonce-", "cap.example.com", "'unsafe-inline'"} {
		if strings.Contains(csp, unwanted) {
			t.Errorf("non-login admin page CSP contains %q: %s", unwanted, csp)
		}
	}
	if want := "default-src 'self'"; !strings.Contains(csp, want) {
		t.Errorf("non-login admin page CSP missing %q: %s", want, csp)
	}
}
