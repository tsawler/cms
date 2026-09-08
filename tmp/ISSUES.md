# Code review findings

Review of the `cms` module: weaknesses, dead code, and duplication.
Tooling run: `go vet ./...` (clean), `staticcheck ./...` (two unused
assignments in `admin/twofactor_db_test.go` only), `deadcode ./...`, and
`go test -race ./...` (green).

Automated tooling is clean, and there is essentially **no dead code** —
every declaration in the module has at least one reference. Duplication is
also low. What follows is mostly design and security weakness.

Status key: `[ ]` open, `[x]` fixed, `[~]` partially addressed, `[-]` won't fix.

---

## Security

### [x] 1. Password-reset links are built from the `Host` header

`admin/handlers_reset.go:118,223` + `posts.go:187`

`absoluteAdminURL` falls back to `scheme + "://" + r.Host` when
`Config.SiteURL` is unset, and `SiteURL` is unset by default:
`ConfigFromEnv` reads `CMS_SITE_URL`, but the scaffold's `.env` never
writes it, and the docs describe it only as a convenience for "RSS item
links and hreflang alternates" — password-reset links aren't mentioned.

An attacker submits the forgot-password form for a victim admin's address
with `Host: attacker.example`. The victim gets a genuine email whose link
points at the attacker, and clicking it hands over a live reset token.
Confirmed that Go's server accepts arbitrary `Host` values (it even
accepts `"`, `<`, `>`).

The throttle and CAPTCHA slow this but don't stop it. Fixes: refuse to
send when `SiteURL` is empty, or validate `Host` against an allowlist.
The same untrusted `Host`/`X-Forwarded-Proto` reaches hreflang and RSS
links, but those are escaped, so no XSS.

**Fixed.** `admin.Deps` gained `SiteURL` — the host's *configured* base,
never mixed with the request — and the reset flow now resolves its base
through a new `emailBaseURL`, which returns `SiteURL` when set, the
request's own base only when the Host is loopback (a developer on their
own machine), and otherwise nothing at all. `forgotRequest` resolves it
*before* minting, so a declined request cannot be used to keep revoking
somebody's live token, and still renders the identical confirmation page
so no new oracle appears. `X-Forwarded-Proto` is no longer consulted on
that path. `cms.New` warns at startup when a `Mailer` is configured and
`SiteURL` is not; the scaffold's `.env`, the QUICKSTART production
checklist, and both `CMS_SITE_URL` doc tables now say why it matters.

Tests: `admin/reset_baseurl_test.go` (table over `emailBaseURL`, including
near-miss hosts like `localhost.attacker.example`), plus
`TestResetLinkIgnoresHostHeader` and `TestResetLinkUsesConfiguredSiteURL`
in `admin/reset_db_test.go`. `TestResetFlowEndToEnd` used to rewrite the
link's host before following it, which made it pass no matter where the
link pointed; it now asserts the host instead. All verified to fail
against the pre-fix code.

### [x] 2. The SVG upload scan misses SMIL scripting vectors

`media/svg.go:108`

`checkSVGToken` rejects `<script>`/`<foreignObject>`, `on*` attributes,
and `javascript:` in `href`. It does not look at `to=` / `values=` /
`from=`, which is how SMIL rewrites an attribute at runtime. All four of
these returned `nil` from `processSVG`:

```
set-href   <a href="#x"><set attributeName="href" to="javascript:alert(1)"/>…    err=<nil>
animate    <a><animate attributeName="href" values="javascript:alert(1)"/>…      err=<nil>
handler    <handler ev:event="load">alert(1)</handler>                            err=<nil>
use-data   <use href="data:image/svg+xml;base64,…"/>                              err=<nil>
```

The media proxy's `default-src 'none'` CSP (`cms.go:1674`) blocks
execution — but only on the proxy path. With `S3Config.PublicRead` or
`PublicBaseURL`, `PublicURL` hands out direct bucket/CDN URLs
(`media/objectstore.go:435`) that carry no CSP, and then a visitor
navigating to the SVG gets script on that origin. Worth rejecting
`attributeName` referring to `href`/`xlink:href` (or any `to`/`values`/
`from` carrying a scheme) at upload time.

**Fixed.** `checkSVGToken` was restructured from an inline denylist into
three named rules with stated reasons: `scriptableElements`
(script/foreignObject/handler), `animationElements` (the SMIL family), and
`svgValueAttrs` — `href` plus the SMIL value attributes `to`/`from`/`by`/
`values`, which are the ones an animation writes *into* another attribute.
The scheme check (now `dangerousSVGURL`) runs over all of them, splitting
SMIL's `;`-separated value lists so a payload later in the list is still
seen. Two deliberately overlapping guards cover the animation family: by
what it aims at (`attributeName` naming href or xlink:href) and by what it
writes (the value check). XML Events is closed on both halves — the
`handler` element and the `ev:event`/`ev:handler` attributes. `data:image/`
no longer waves through `data:image/svg+xml`, which says image and means
document.

Tests: ten new rejection cases in `TestProcessSVGRejectsActiveContent`,
all verified to pass (i.e. fail the test) against the old scanner. Six new
*acceptance* cases guard the other direction — ordinary `<animate>`,
`<animateTransform>` and `<set>` on `opacity`/`transform`/`fill`, and real
links — because a scan that rejected a designer's spinning logo would get
switched off, which is worse than the hole. Those passed under both the
old and new scanner, confirming no over-blocking was introduced.

Not addressed here, and split out as #23: the scan is the *only* layer on
a deployment that serves media straight from a public bucket.

---

### [x] 23. An SVG served straight from a public bucket has no CSP behind it

Split out of #2. The media proxy sets
`Content-Security-Policy: default-src 'none'` on `image/svg+xml`
(`cms.go:1674`), which is what stops anything the upload scan missed from
executing. That header is the CMS's own response, so it exists only on the
proxy path: with `S3Config.PublicRead` or `PublicBaseURL`, `PublicURL`
hands out direct bucket/CDN URLs (`media/objectstore.go:435`) and the
scan in #2 is the whole defense.

Worth considering: storing SVG objects with
`Content-Disposition: attachment`. It does not affect `<img src>` or CSS
`url()` rendering — only top-level navigation, which is exactly the case
where a stored SVG would run as a document. Cheap, but it changes object
metadata (so it applies to new uploads only) and makes the media
library's "Copy link" download rather than display, so it wants a
deliberate decision rather than being folded into #2.

**Fixed**, but not the way sketched above. `Content-Disposition` would
have needed an optional-interface dance around the exported
`ObjectStore.Put`, would only have covered new uploads, and would have
made "Copy link" download rather than display. Routing SVG through the
proxy is smaller and stronger: all four public-URL calls already funnel
through `Manager.URL`, so a single `Manager.publicURL` helper returns the
`/cms/media/…` path for a ".svg" key and defers to the store for
everything else. It applies to existing objects immediately, needs no
interface change, and restores the CSP layer rather than substituting a
different one.

Keyed off the object key rather than the record, since the key is what the
proxy resolves and ".svg" keys come from the SVG pipeline alone (`docTypes`
has no `.svg`). Raster images, videos and documents keep their CDN URLs —
moving all media off the CDN to fix SVG would be a much larger change than
the problem.

Residual: the object still exists at its bucket URL under `PublicRead`.
Reaching it needs the key, which is 12 random bytes, is no longer emitted
anywhere, and is not enumerable through the policy `ApplyPublicReadPolicy`
writes (it grants `s3:GetObject` only, not `s3:ListBucket`).

Tests: `TestURLKeepsSVGOnTheProxy` and
`TestURLKeepsSVGOnTheProxyUnderAKeyPrefix` (unit, against a store that
hands out CDN URLs) and `TestPublicStoreStillProxiesSVG` (end to end —
upload to a public store, then fetch each rendition and assert the CSP is
actually on the response). All verified to fail without the fix. Each
asserts the other direction too: a PNG, video and PDF on the same store
must keep their direct URLs.

One existing test changed: `TestImageForVectorsAndNonImages` asserted
`/media/vec001/web.svg`, which was the stub store's `"/"+key` convention
rather than a CMS guarantee. It now asserts the proxy path and says why.

### [ ] 3. Login and 2FA throttles are keyed by IP, so neither caps an account

`admin/handlers_auth.go:33` (`email|ip`) and `:203` (`2fa|userID|ip`).

The comment at :201 says "five tries per fifteen minutes is what makes
guessing not a strategy" — but the key includes the IP, so it's five
tries *per IP*. An attacker with a /64 of IPv6 has unlimited keys. For
TOTP that matters: three codes of 10^6 are valid at any moment, so
~330k requests is a real attack.

The mirror-image problem: behind a reverse proxy `remoteIP` returns the
proxy's address for everyone, which collapses the key — five failed
logins lock a known admin email out for everybody. Consider a second,
IP-independent per-account counter alongside the existing one.

### [ ] 4. The site lock isn't wired up in anything the project generates

`Lockdown` is the outer door for `SiteSettings.Locked` — but
`scaffold/files/main.go.tmpl` and both `examples/*/main.go` do
`mux.Handle("/", c.Handler())` with no `Lockdown` wrapper. `Handler()`
itself does no lock check. So a superadmin who throws the switch in the
editor's Site settings dialog gets the admin locked down and the public
site still fully served. It's documented in `docs/production.md:215`, but
the switch is in the UI and the generated app doesn't honour it.

### [ ] 5. `SecureCookies` is the one production setting with no environment knob

`cms.go:713` — `sessions.Cookie.Secure = cfg.SecureCookies`, default
`false`. `ConfigFromEnv` exposes ~15 variables and not this one; the
scaffold's `main.go` (which configures everything else from env) never
sets it. A scaffolded site deployed behind HTTPS ships a non-`Secure`
session cookie unless the developer finds QUICKSTART step 10 and edits Go
source. Either add `CMS_SECURE_COOKIES`, or default to secure and let
hosts opt out.

### [ ] 6. `S3_APPLY_PUBLIC_POLICY` is all cost, no benefit from the environment

`env.go` sets `ApplyPublicReadPolicy` but not `PublicRead` or
`PublicBaseURL`. The field's own doc says "pair it with PublicRead or
PublicBaseURL so pages actually embed direct bucket URLs" — which is
impossible from env. An env-configured deployment that sets it makes the
bucket world-readable *and* still proxies every byte through the app,
gaining nothing and losing the SVG CSP from finding #2.

### [ ] 7. Scaffolded sites default to `admin@example.com` / `password123`

`scaffold/files/main.go.tmpl:137`. It logs a warning, but an install
deployed without `CMS_ADMIN_PASSWORD` has a guessable superadmin.
Generating a random password and printing it once would cost nothing.

### [ ] 8. The last superadmin can demote themselves, permanently

`admin/handlers_users.go:150` guards only
`self.Role.IsAdmin() && !form.Role.IsAdmin()` — superadmin → admin passes
both sides. Only a superadmin can *assign* superadmin
(`parseUserForm:456`), and `SeedAdmin` is a no-op once any user exists
(`cms.go:961`). So the site loses snippets, the Pages section, masquerade
and the lock override with no in-app recovery — only a direct DB edit.

### [ ] 9. Sitemap cache is keyed on the attacker-controlled `Host`

`sitemap.go:67` — `c.sitemapBase != base` invalidates the cache, so one
unauthenticated request per distinct `Host` header forces a full rebuild
(up to 50k rows plus XML marshal) *and* evicts the cached copy. Cheap to
send, expensive to serve.

### [ ] 10. CAPTCHA fail-open is wider than the comment claims

`captcha/captcha.go:154`. The comment says it fails open on outage, but
`Verify` also returns an error on a JSON decode failure, and both call
sites (`handlers_auth.go:54`, `handlers_reset.go:84`) treat any error as
"skip the check". A 200 with a malformed body is a received response, not
an unreachable server, and arguably should be a rejection.

---

## Robustness and efficiency

### [ ] 11. `currentUser` is unmemoized — 47 call sites, 2 queries each

`admin/middleware.go:43` does `GetByID` + `loadPermissions` on every call,
with no request-scoped cache. A `GET /users` runs it in `requireUser`,
`requirePerm`, the handler, `newTemplateData`, and `canUseMedia` — five
calls, ten queries, for one page. (The users list itself is fine;
`canManageUser` was correctly hoisted out of the row loop.) One
`context.WithValue` in `requireUser` would collapse this.

### [ ] 12. Site settings are read twice per public page render, and can disagree

`siteFlags` (`cms.go:1269`) exists specifically so the mode isn't a query
per request — then `servePage:1406` calls `c.content.SiteSettings()`
directly and uncached. Two consequences: the cache buys nothing on the
page path, and within one response `site.dev` from the 5s cache can set
`X-Robots-Tag: noindex` while the fresh read renders no `<meta robots>`.
`siteFacts` is a subset of `SiteSettings` — cache the whole struct once.

### [ ] 13. All session I/O runs on `context.Background()`

`internal/sessionstore/sessionstore.go:40,55,64` and
`internal/redisstore/redisstore.go:32,48,53` implement scs's non-context
`Store`. scs v2.9 offers `CtxStore` (`FindCtx`/`CommitCtx`/`DeleteCtx`)
and prefers it when present. As written, a session read has no deadline
and ignores client disconnects — under DB stress those queries pile up
unbounded.

### [ ] 14. No `CMS.Close()`

`sessionstore.New` starts an hourly cleanup goroutine and exposes
`StopCleanup`, but `cms.New` (`cms.go:706`) neither keeps a reference nor
offers a shutdown method. Same for the Redis client. Every `cms.New`
leaks a goroutine — invisible in production, but it accumulates in tests
and in hosts that rebuild the CMS.

### [ ] 15. Error classification by substring match

`tailwind.go:313` — `strings.Contains(err.Error(), "no rows")` instead of
`errors.Is(err, sql.ErrNoRows)`. If it ever stops matching,
`loadContentCSS` returns an error, `buildOnce` bails, and the stylesheet
silently stops rebuilding. Same pattern at `admin/handlers_media.go:228-229`
and `handlers_api.go:1228` (`"decoding image"`, `"parsing svg"`) — those
should be sentinel errors from the `media` package.

### [ ] 16. Upload `MaxBytesReader` calls are dead on the form path

`handlers_media.go:206`, `handlers_api.go:1196,1258` set
`r.Body = http.MaxBytesReader(...)` — but `readToken`
(`middleware.go:294`) has already called `ParseMultipartForm` for
form-posted requests, and its own comment says any later limit "is dead
code that reads as if it works." These are live only for the JS uploader's
header path. Either drop them or comment why they're conditional; as
written they read as the enforcement and aren't.

### [ ] 17. Two env parsers silently ignore bad values

`CMS_MEDIA_MAX_VIDEO_MB` and `CMS_MEDIA_WEBP_QUALITY` (`env.go:150,158`)
check only the parse error; negative/out-of-range values pass through and
are then discarded by `SetMaxVideoBytes`/`SetWebPQuality`. Every other
variable in that file rejects out-of-range values with a clear message.

---

## Duplication

### [ ] 18. Page-write logic is forked between `page.go` and `post.go`

`content.InsertPost` (`post.go:173`) reimplements `Insert`'s
(`page.go:334`) three statements — `cms_pages`, `cms_page_drafts`,
`cms_page_meta` — and `UpdatePost` does the same for `Update`. The
divergence is already there: `Insert` writes `visibility` explicitly,
`InsertPost` omits it and relies on the column default. This is the
duplication most likely to bite; a shared `insertPageRows(tx, …)` would
fix it.

### [ ] 19. `requirePerm` is a pure alias

`admin/middleware.go:149-151` — `return s.requireAnyPerm(p)`. Either drop
it and call `requireAnyPerm` at the ~4 sites, or keep it and delete the
doc comment that describes it as a distinct thing.

### [ ] 20. `escapeLike` vs `likeEscaper`

`content/search.go:452` and `auth/user.go:166` — identical `\`/`%`/`_`
replacer, written twice. The `content` copy also allocates a fresh
`strings.NewReplacer` on every call instead of using a package-level var.

### [ ] 21. `absoluteAdminURL` reimplements `requestBaseURL`

`admin/handlers_reset.go:228-233` vs `posts.go:187` — same `r.TLS` /
`X-Forwarded-Proto` / `r.Host` logic in two packages. The admin already
receives `Deps.SiteBaseURL`; the fallback branch is the duplicate, and
it's the branch that carries finding #1.

### [ ] 22. Page/post handler triples

`handlers_pages.go:587-643` vs `handlers_posts.go:183-232` —
`pageDelete`/`pageDiscard`/`pageUnpublish` and their post equivalents are
structurally identical, differing only in the loader, the noun in the
flash, and the redirect base. `handlers_versions.go` already shows the
shared shape works (`renderVersions`, `restoreVersion` take a
`base string`). Lower value than #18 — the messages genuinely differ —
but it's the largest remaining copy-paste in the admin.

---

## Suggested order

1. #1 (reset-link poisoning)
2. #2 (SVG SMIL)
3. #4 / #5 / #7 together — "the generated site isn't production-safe by default"
4. #11 and #12 — cheapest performance wins
5. #18 — the duplication worth paying down
