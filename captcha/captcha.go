// Package captcha verifies proof-of-work CAPTCHA tokens against a
// self-hosted Cap server (https://capjs.js.org, docker image tiago2/cap).
// The CMS uses it to protect the admin login form; host applications may
// reuse the client for their own forms.
//
// Cap has two halves: a browser side that solves the challenge and puts the
// resulting token in a hidden cap-token form field — either the visible
// <cap-widget> checkbox or, by default here, the invisible programmatic mode
// (see Config.Visible) — and a server that issues challenges and verifies
// solutions. The browser script itself is served by the Cap server, so no
// third-party CDN is involved.
package captcha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FieldName is the hidden form field the Cap widget stores its token in.
const FieldName = "cap-token"

// Config locates the Cap server and identifies the site to it. Create site
// keys in the Cap dashboard (log in with the server's ADMIN_KEY).
type Config struct {
	// URL is the browser-facing base URL of the Cap server, e.g.
	// "https://cap.example.com" or "http://localhost:3000". The widget
	// script and challenge API are loaded from here. Required.
	URL string

	// InternalURL is the base URL the application server uses for
	// server-to-server token verification, when that differs from URL —
	// e.g. "http://cap:3000" inside a Docker network. Defaults to URL.
	InternalURL string

	// SiteKey identifies the site to the Cap server. Required.
	SiteKey string

	// Secret authorizes siteverify calls for the site key. Required.
	Secret string

	// Visible renders Cap's interactive checkbox widget on the login
	// form. The default (false) uses Cap's programmatic mode instead:
	// the login page solves the challenge invisibly in the background
	// and submits the token with the form, so users never see a
	// CAPTCHA at all.
	Visible bool
}

// Client verifies Cap tokens and produces the URLs the login page needs to
// embed the widget.
type Client struct {
	cfg    Config
	origin string
	http   *http.Client
}

// New validates cfg and returns a ready Client.
func New(cfg Config) (*Client, error) {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	cfg.InternalURL = strings.TrimRight(cfg.InternalURL, "/")
	if cfg.URL == "" {
		return nil, errors.New("captcha: Config.URL is required")
	}
	if cfg.SiteKey == "" {
		return nil, errors.New("captcha: Config.SiteKey is required")
	}
	if cfg.Secret == "" {
		return nil, errors.New("captcha: Config.Secret is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("captcha: Config.URL %q is not an absolute URL", cfg.URL)
	}
	if cfg.InternalURL == "" {
		cfg.InternalURL = cfg.URL
	}
	return &Client{
		cfg:    cfg,
		origin: u.Scheme + "://" + u.Host,
		http:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// ScriptURL is the widget script's URL, served by the Cap server itself.
func (c *Client) ScriptURL() string {
	return c.cfg.URL + "/assets/widget.js"
}

// WidgetEndpoint is the value for the widget's data-cap-api-endpoint
// attribute.
func (c *Client) WidgetEndpoint() string {
	return c.cfg.URL + "/" + c.cfg.SiteKey + "/"
}

// WasmURL is the solver's WebAssembly binary, served by the Cap server.
// The widget defaults to fetching it from a public CDN; pointing
// window.CAP_CUSTOM_WASM_URL here keeps everything self-hosted (and inside
// the CSP) — without it the widget falls back to a many-times-slower
// pure-JS solver.
func (c *Client) WasmURL() string {
	return c.cfg.URL + "/assets/cap_wasm_bg.wasm"
}

// PakoPath is the admin-relative path of the vendored pako library, which
// the widget's instrumentation step decompresses with.
//
// Like the WASM binary, the widget defaults to a public CDN for this; the
// admin CSP allows scripts only from the app and the Cap server, so the
// fetch would be blocked and the solver would stall at the final step with
// "Instrumentation timed out". Pointing window.CAP_PAKO_URL at our own copy
// keeps it inside the policy.
const PakoPath = "/static/pako_inflate.min.js"

// Origin is the browser-facing origin of the Cap server, for building a
// Content-Security-Policy that admits the widget.
func (c *Client) Origin() string {
	return c.origin
}

// Visible reports whether the login form should show Cap's interactive
// checkbox widget rather than solving the challenge invisibly.
func (c *Client) Visible() bool {
	return c.cfg.Visible
}

// Verify checks a widget token with the Cap server. It returns (false, nil)
// when the server rejects the token and a non-nil error only when the
// server could not be consulted at all — callers decide whether that fails
// open or closed.
func (c *Client) Verify(ctx context.Context, token string) (bool, error) {
	body, err := json.Marshal(map[string]string{
		"secret":   c.cfg.Secret,
		"response": token,
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.InternalURL+"/"+c.cfg.SiteKey+"/siteverify", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Cap rejects tokens with 4xx statuses ({"success": false} plus an
	// error string), and accepts them with 200 {"success": true} — so a
	// 4xx is a verdict, not a fault. Only 5xx means we got no verdict.
	if resp.StatusCode >= 500 {
		return false, fmt.Errorf("captcha: siteverify returned status %d", resp.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("captcha: decoding siteverify response: %w", err)
	}
	return out.Success, nil
}
