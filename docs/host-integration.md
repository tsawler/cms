# Host application integration

Mounting your own pages inside the admin area, and the contract between the CMS and the application that embeds it.

## Custom admin pages

Deployments often need admin pages the CMS doesn't ship — reports,
imports, integration settings. Register them with `Config.AdminSections`:
each section is a plain `http.Handler` the CMS mounts at
`{AdminPath}/x/{Path}/` **inside** the admin's middleware chain, so
login enforcement, sessions, CSRF validation, and security headers are
guaranteed — a custom page can't accidentally ship without them.

```go
AdminSections: []cms.AdminSection{
    {Path: "reports", NavLabel: "Reports", Handler: reportsHandler},
    {Path: "billing", NavLabel: "Billing", AdminOnly: true, Handler: billingHandler},
},
```

- **`Path`** — one URL segment of letters, digits, or `- . _ ~`; the
  section root is served at `{AdminPath}/x/reports/` (the `/x/` namespace
  guarantees host sections never collide with built-in admin routes, now
  or after upgrades). The bare URL without the trailing slash redirects
  to the slashed form, so relative links inside the section resolve
  correctly.
- **`NavLabel`** — adds a link to the admin top bar; leave empty for
  routable-but-unlisted pages.
- **`NavAfter`** — places the nav link directly under a built-in sidebar
  entry: `"dashboard"`, `"pages"`, `"posts"`, `"media"`, `"snippets"`, or
  `"users"`. Leave empty for the default position, after the built-in
  entries. Sections naming the same anchor keep their registration order
  — the wheels example anchors all six of its sections to `"dashboard"`,
  so the dealership's own tools top the sidebar.
- **`NavCount`** — a `func(context.Context) (int, error)` supplying the
  number beside the nav link, with the same leader line as the built-in
  entries. Called on every admin page render, so keep it a cheap query;
  an error is logged and renders as zero. Leave nil for no count — right
  for links that trigger an action rather than open a list. The wheels
  example counts its vehicles, active sales people and staff, leads, and
  push-ready feeds this way (`navcounts.go`).
- **`Dashboard`** — puts a card for the section on the admin dashboard,
  ahead of the built-in cards, linking to the section root. A
  `cms.DashboardCard` carries a `Title` (defaults to `NavLabel`), a
  one-line `Description`, a `Count` func run once per dashboard render
  (defaults to `NavCount`; the same error-renders-as-zero contract),
  and an optional `Note` func supplying a short dynamic line under the
  description — freshness or urgency in the host's words ("Oldest
  undelivered: 2 days"; errors log and show nothing). Give the card the
  number that asks for attention — the wheels example's Vehicles card
  counts what still needs making ready, while its nav link counts the
  whole lot. Visibility follows the section's own rules (`AdminOnly`,
  `Permission`, `AdminsNeedGrant`).
- **`AdminOnly`** — editors get 403 and no nav link.
- **`Permission`** — restricts the section to users holding the named
  permission (see [Roles & permissions](users-and-permissions.md#roles--permissions)). Naming a
  built-in permission reuses it; any other key *declares* a custom
  permission, which appears as a checkbox on the admin's user form,
  labelled with `NavLabel`. The wheels example gates its inventory
  manager this way: `{Path: "inventory", NavLabel: "Inventory",
  Permission: "vehicles", Handler: ...}`.
- **`AdminsNeedGrant`** — makes `Permission` bind the admin role too:
  admins see and open the section only with the grant ticked on their
  user page, exactly as editors do; superadmins always pass. Several
  sections may share one permission key to switch on and off together —
  the wheels example groups its Sales people and Staff sections under a
  single `"team"` grant this way.
- **`Handler`** — sees section-relative paths (`/` at the root), so it can
  serve sub-routes and its own static assets beneath it.

`cms.New` rejects a config with malformed, duplicate, or handler-less
sections; call `admin.ValidateSections` yourself to fail even earlier
(e.g. in a test).

**Styling the chrome your sections appear in.** A section's own
stylesheet is linked from its own pages, so it cannot reach the sidebar,
which renders on every admin page — and the admin's CSP forbids an
inline `<style>`. `Config.AdminStylesheets` links a host stylesheet on
every admin page, after the admin's own so its rules win at equal
specificity:

```go
AdminStylesheets: []string{"/admin/x/registrations/adminassets/nav.css"},
```

Serve the file from a section's own asset route. The usual reason is a
`NavCount` that means something other than "how many": every built-in
count is a total, so a count of what is *outstanding* reads wrong as a
bare number and wants a badge. Nav links carry no per-section class, so
target them by href — `.cms-nav a[href$="/x/registrations/"]
.cms-nav-count`.

Inside a handler, four helpers from `github.com/tsawler/cms/admin`
integrate with the admin UI:

| Helper | Purpose |
|---|---|
| `admin.UserFrom(r)` | the logged-in `*auth.User` (never nil in a section) |
| `admin.CSRFToken(r)` | token for the `csrf_token` field in POST forms |
| `admin.SetFlash(r, msg)` | one-time message on the next admin page load |
| `admin.RenderPage(w, r, title, body)` | wrap trusted HTML in the admin chrome |

```go
func reportsPage(w http.ResponseWriter, r *http.Request) {
    user := admin.UserFrom(r)   // logged-in *auth.User, never nil here
    body := fmt.Sprintf(`<h1>Reports</h1>
        <p>Hello %s.</p>
        <form method="post" action="refresh">
            <input type="hidden" name="csrf_token" value="%s">
            <button type="submit" class="cms-btn">Refresh</button>
        </form>`,
        template.HTMLEscapeString(user.Name),
        template.HTMLEscapeString(admin.CSRFToken(r)))

    // Wraps body (trusted host HTML) in the admin chrome: top bar,
    // nav, flash messages, stylesheet.
    admin.RenderPage(w, r, "Reports", template.HTML(body))
}

func refresh(w http.ResponseWriter, r *http.Request) {
    // CSRF was already validated before this ran.
    admin.SetFlash(r, "Refreshed.") // shown on the next admin page load
    http.Redirect(w, r, admin.SectionPath(r), http.StatusSeeOther)
}
```

Things to know:

- **POST forms need the CSRF token.** The admin middleware rejects unsafe
  methods without it; include `admin.CSRFToken(r)` as the `csrf_token`
  field (or an `X-CSRF-Token` header from JS).
- **No inline scripts or styles.** The admin serves a strict
  Content-Security-Policy without `unsafe-inline`; serve JS/CSS as files
  from the section's own handler. With an embedded FS that's one route:

  ```go
  //go:embed assets
  var assetsFS embed.FS

  mux := http.NewServeMux()
  mux.Handle("GET /assets/", http.FileServerFS(assetsFS))
  // referenced from the page as <script src="assets/app.js" defer></script>
  ```
- **Redirect with full paths.** The handler sees stripped paths, so
  `http.Redirect` with a relative URL resolves against the wrong base;
  use `admin.SectionPath(r)`, the section's browser-facing base URL
  (e.g. `/admin/x/reports/`), appending a segment for sub-routes.
- `admin.RenderPage` always writes 200 and inserts `body` unescaped —
  it's your code's HTML, escape any user data you interpolate into it.
  For other status codes or a fully custom look, write the response
  directly; the CSS classes in `admin.css` (`cms-btn`, `cms-card`,
  `cms-muted`, ...) are available either way.

The example app registers a working section — see `reportsSection` in
`examples/basic/main.go`.

For *modifying* built-in admin pages there is deliberately no template
override mechanism (it would couple deployments to internal template
data and break silently on upgrades). The supported paths are the
existing configuration knobs (`Snippets`, `EditorStyles`,
`SectionStyles`, ...) — and when those don't cover a need, a config
option added to the CMS itself.

## Notes for host applications

- **Style your rich regions.** The CMS never injects styles into your
  pages, so headings, lists, and blockquotes created by editors look
  however *your* CSS says. With Tailwind this matters: Preflight resets
  h1–h6/ul/blockquote to plain text, so give rich regions typography
  styles (e.g. the `@tailwindcss/typography` plugin's `prose` class, as
  `examples/basic` does) or editors' formatting will be invisible.
- **Safelist the editor's style classes** — see
  [The Styles menu](../QUICKSTART.md#6-style-the-site) above; skipping this
  makes applied styles silently invisible in production Tailwind builds.
- Template file extensions are the host's choice (`.gohtml` plays best
  with editor tooling) — the CMS loads whatever paths you configure. But
  pages store their template's path, so renaming template files under an
  existing database needs a one-time fixup, e.g.
  `UPDATE cms_pages SET template_name = replace(template_name, '.tmpl', '.gohtml');`
- All tables are prefixed `cms_`, so the CMS can share a database with the
  host app.
- **Login sessions can live in Redis instead of the database.** Set
  `Config.Redis` (or `CMS_SESSION_REDIS_ADDR`) and sessions move from the
  `cms_sessions` table to Redis under `cms_session:` keys, with Redis's own
  key expiry replacing the hourly cleanup sweep. Everything else stays in
  the database; leave it unset and sessions do too.
- `Migrate` is safe to run on every startup and from multiple instances
  concurrently. On MySQL and MariaDB it is not transactional — see
  [Schema](../DESIGN.md#data-model-postgres).
- **`Config.ObjectStore` replaces S3 entirely.** Implement
  `media.ObjectStore` (`Put`/`Get`/`Delete`/`PublicURL`) and the media
  library uses it instead of a bucket — local disk in development, or any
  storage you already run. When set, `Config.S3` is ignored.
- A few `Config` fields have no environment variable and are easy to miss:
  `SessionLifetime` (default 24h; `RememberFor` extends it for "Remember
  me"), `Logger` (defaults to `slog.Default()`), and `ObjectStore` above.
  The struct's godoc documents every field.
- SVG uploads are accepted as images (they act as their own renditions at
  every size — no rasterizing). Because an SVG viewed directly
  is a document that can run scripts, uploads are rejected unless they
  are free of active content (`<script>`, `<foreignObject>`, `on*`
  attributes, `javascript:`/non-image `data:` hrefs, DTD internal
  subsets), and the media proxy serves `image/svg+xml` with a
  script-blocking `Content-Security-Policy`. If you serve media straight
  from a public bucket/CDN instead of the proxy, that header is in your
  hands — configure it there if SVG uploads concern you.
- Set `Config.SecureCookies = true` in production (HTTPS).
- `Config.AdminPath` (default `/admin`) is where `Handler()` serves the
  admin area. If you wire `Admin()` yourself instead, the mount point
  must match it.
