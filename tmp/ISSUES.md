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

### [x] 3. Login and 2FA throttles are keyed by IP, so neither caps an account

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

**Fixed.** Three counters now, because "too many attempts" was three
questions being asked with one key (`throttleLimits` in
`admin/middleware.go` carries the reasoning and the arithmetic):

- per account **and** source, 5 per 15 min — unchanged, still the tight
  leash on any one origin.
- per account, any source, **25** per 15 min — passwords (login and
  forgot-password). Deliberately loose: any per-account limit is also a
  lockout lever, so it sits where no human lands on it and an attacker
  must sustain 100 failures an hour against one named account to hold it.
  This one is a trade, not a free win, and says so in the comment.
- per account, any source, **10** per 15 min — two-factor codes. The
  strict one, and the reason the change exists: 3-in-10^6 per guess and
  ~231,000 guesses for even odds means an unmetered attacker walks the
  space in days, where 960/day is ~8 months of continuously logged
  failure. The lockout objection barely applies, since anyone able to
  trip it already has the password.

A correct password clears both password counters and deliberately does
*not* touch the code counters — otherwise whoever is guessing codes (who
by definition has the password) could refill their allowance at will.

The proxy half is a new `Config.ClientIPHeader` / `CMS_CLIENT_IP_HEADER`,
consumed by `server.clientIP`. Unset behind a proxy the per-source counter
was not merely imprecise but inverted — every visitor shared one key, so
five failures shut an account for everyone. It has to be explicit
configuration: trusting a header by default would let an attacker draw a
fresh allowance per request, which is worse than the bug. For
X-Forwarded-For the rightmost entry is used; an absent header falls back
to the connection rather than to a blank key.

Tests: `admin/clientip_test.go` (13 cases — a client prepending entries to
XFF must not move the answer; absent/empty/whitespace/trailing-comma fall
back to the connection), `admin/throttle_scope_test.go` (rotating source
still hits the account limit; one account's lockout does not spread; the
per-source limit still bites), and `TestTwoFactorCodeLimitSurvivesRotatingSources`
/ `TestTwoFactorCodeLimitSurvivesReLogin` in `admin/twofactor_db_test.go`.
The attack is spelled by varying `ClientIPHeader` input rather than by
opening sockets from different addresses, which is the same thing as far
as `clientIP` — the one place a source is decided — is concerned.

All verified to fail with the per-account counters removed. The re-login
test was additionally verified against a deliberately introduced
`acctCode.Reset` in the password path, which is the specific bypass it
exists to catch.

### [x] 4. The site lock isn't wired up in anything the project generates

`Lockdown` is the outer door for `SiteSettings.Locked` — but
`scaffold/files/main.go.tmpl` and both `examples/*/main.go` do
`mux.Handle("/", c.Handler())` with no `Lockdown` wrapper. `Handler()`
itself does no lock check. So a superadmin who throws the switch in the
editor's Site settings dialog gets the admin locked down and the public
site still fully served. It's documented in `docs/production.md:215`, but
the switch is in the UI and the generated app doesn't honour it.

**Fixed**, and not only in the generated files. Wiring `Lockdown` into the
scaffold would have left the same trap for every hand-written host, so the
enforcement moved to where it cannot be forgotten: `Pages()` now refuses
on its own account, via `refuseLocked`. The switch is in the product's own
UI, so it closes the site's pages, feeds, sitemap, robots.txt, proxied
media and editor bundle on a stock install, with nothing mounted.

`Lockdown` keeps the two jobs the CMS cannot do from inside its own
handler: closing routes the CMS does not serve, and holding the exempt
list. The scaffold and both examples now wrap with it, so the generated
app is complete and the API is demonstrated where someone will see it.

The two layers had to be made not to fight. `Lockdown` now marks every
request it passes as already judged (`withLockCleared`), and
`refuseLocked` honours the mark. Without it the inner check would
re-adjudicate — a second session read per request on a locked site, and,
worse, it would refuse the very addresses the host had just exempted. An
exempt CMS address (a partner's feed at /blog/rss.xml) is exactly the case
that would have broken.

Tests: `TestPagesEnforceTheLockWithoutLockdown` (no wrapper anywhere —
pages, robots.txt, sitemap and media all 503 with Retry-After and
no-store; editor refused, superadmin served),
`TestHandlerEnforcesTheLockAndKeepsTheAdminOpen` (the way back in stays
open), and `TestLockdownExemptSurvivesPagesEnforcement`. All verified to
fail with `refuseLocked` removed; the exempt test additionally verified
against a `Lockdown` that forwards without marking, which is the specific
regression it guards.

One test bug found and fixed while writing them: exempting `"/"` matches
the whole site, because the exempt rule reads a trailing slash as a
prefix. The test now names a distinct published page, so it distinguishes
exempt from non-exempt instead of passing on a technicality.

`docs/production.md` said "The setting does nothing until the host wraps
its router", which is no longer true; that section now separates what the
switch does by itself from what `Lockdown` adds.

### [x] 5. `SecureCookies` is the one production setting with no environment knob

`cms.go:713` — `sessions.Cookie.Secure = cfg.SecureCookies`, default
`false`. `ConfigFromEnv` exposes ~15 variables and not this one; the
scaffold's `main.go` (which configures everything else from env) never
sets it. A scaffolded site deployed behind HTTPS ships a non-`Secure`
session cookie unless the developer finds QUICKSTART step 10 and edits Go
source. Either add `CMS_SECURE_COOKIES`, or default to secure and let
hosts opt out.

**Fixed**, both ways, because the env variable alone would only have moved
the remembering rather than removed it. The flag is now *derived*: an
`https://` `SiteURL` means the site is served over HTTPS by the host's own
account, so the cookie is marked Secure whether or not anyone also sets
`SecureCookies`. This is the setting whose absence is silent — nothing
looks broken, the site works, and the admin session cookie is one
plaintext request away from being readable — so it should not depend on a
second, separate act of memory. `#1` had just made `SiteURL` effectively
required in production, which is what makes the signal reliable.

`SecureCookies bool` keeps its meaning (force it on) rather than becoming
a `*bool`, so nothing breaks; it now covers only the case the derivation
cannot see, HTTPS in front of an install that leaves `SiteURL` empty.
There is deliberately no way to turn the flag *off* for an `https://`
site: Secure describes the browser's connection to the edge, not the
edge's to this process, so terminating TLS at a proxy is not a reason to
drop it.

`CMS_SECURE_COOKIES` added as well, parsed like `CMS_SITE_LOCKED` — a
malformed value is an error rather than a silent false, since
`CMS_SECURE_COOKIES=yes` quietly meaning "no" is precisely this setting's
failure mode.

Tests: `secure_cookies_test.go` — a table over the derivation, and
`TestNewMarksTheSessionCookieSecure`, which drives real `New()` calls and
checks the flag reaches the cookie (a rule nothing applies is not a rule).
`sql.Open` does not connect and `New` never queries, so both run without
Docker. Plus `TestConfigFromEnvSecureCookies` for parsing, including the
malformed case. All verified to fail against the pre-fix behaviour.

Found and fixed in passing: `env_test.go`'s `envVars` list — which exists
so a developer's real environment cannot leak into the tests — was missing
`CMS_CLIENT_IP_HEADER`, which #3 had added. Both new variables are in it
now.


### [x] 6. `S3_APPLY_PUBLIC_POLICY` is all cost, no benefit from the environment

`env.go` sets `ApplyPublicReadPolicy` but not `PublicRead` or
`PublicBaseURL`. The field's own doc says "pair it with PublicRead or
PublicBaseURL so pages actually embed direct bucket URLs" — which is
impossible from env. An env-configured deployment that sets it makes the
bucket world-readable *and* still proxies every byte through the app,
gaining nothing and losing the SVG CSP from finding #2.

(The SVG half of that is already gone — #23 keeps SVGs on the proxy
whatever the URL strategy. What was left is a bucket opened to the world
for no benefit at all.)

**Fixed** two ways, because the variable was both unusable and unpaired.

*Made usable:* `S3_PUBLIC_READ`, `S3_PUBLIC_BASE_URL` and
`S3_USE_PATH_STYLE` are now read from the environment, so the whole
serving strategy is expressible there rather than only the half that opens
the bucket. `S3_USE_PATH_STYLE` was the same class of gap and worth
closing at the same time: MinIO is the store the docs point at for local
development and it needs path-style addressing, which env-configured
installs had no way to ask for.

*Made loud:* `NewS3Store` now refuses `ApplyPublicReadPolicy` with neither
`PublicRead` nor `PublicBaseURL`. That combination opens the bucket and
then nothing reads it that way, because `PublicURL` goes on handing out
proxy paths — the site works, so the mistake never announces itself, which
is exactly why it earns a startup error rather than a shrug. Refusing
rather than inferring `PublicRead`: inferring would silently change what
URLs appear in every page's HTML on upgrade, which is a worse surprise
than a message naming the two settings.

Also fixed in passing: `S3_APPLY_PUBLIC_POLICY` was compared against the
literal `"1"`, so `S3_APPLY_PUBLIC_POLICY=true` silently meant *false* —
on the one variable whose job is opening a bucket. All the true/false
variables now go through a shared `envBool` that reports a malformed value
instead of reading it as false, which also removed three spellings of the
same parse (`CMS_SECURE_COOKIES` included).

Tests: `TestNewS3StoreRejectsAPointlessPublicPolicy` (refused alone,
accepted with either pairing, and an ordinary private bucket still fine —
the check is about the policy, not about being private) and
`TestConfigFromEnvS3PublicSettings` (defaults stay private, the base URL
is trimmed, each flag reads `true` and rejects nonsense). `envVars` in
`env_test.go` gained the three new names so the suite stays hermetic. All
verified to fail against the pre-fix code.


### [x] 7. Scaffolded sites default to `admin@example.com` / `password123`

`scaffold/files/main.go.tmpl:137`. It logs a warning, but an install
deployed without `CMS_ADMIN_PASSWORD` has a guessable superadmin.
Generating a random password and printing it once would cost nothing.

**Fixed** in three places, because the credential came from three.

*The scaffold* now generates a password per project (`Options.AdminPassword`
pins it; empty generates) and writes it into `.env`. Four groups of five
characters from an alphabet with the lookalikes removed — 0/O, 1/l/I —
because it is typed by hand at the first login, and "0 or O?" at that
moment is a support ticket.

*The generated main.go* carries no fallback at all. `envOr` was the
problem: a default written into the template is the same on every site
ever generated from it, which is a published credential rather than a
default. It now reads `os.Getenv` and, when unset, seeds nothing and says
so — safe in both directions, since an established site boots normally
(seeding is a no-op anyway) and a new one gets a loud warning naming the
variable. It also no longer logs the password: logs travel further than
configuration does, and whoever set it already knows it.

*The examples* differ deliberately, and the comment says why. A checked-out
example has nowhere a per-project password could have been written — `.env`
there is the developer's own, gitignored file — so refusing would have
broken the documented contributor workflow. They generate one at first run
and print it once instead.

*Also hardened the library.* `SeedAdmin` is the one door into the product
that does not go through a form, so it is the one place a site can get a
superadmin whose password nobody chose. It now rejects anything under
`MinSeedPasswordLength` (8, the same floor the admin's forms use). The
check runs before the account count, so a misconfigured deployment hears
about it on every boot rather than only on the one where it mattered. The
pre-fix behaviour was worse than it looked: `SeedAdmin(ctx, …, "")`
happily created a superadmin whose password was the empty string, which
the new test confirms ("accepted the password \"\"… left 1 user(s)
behind").

Tests: `scaffold/admin_password_test.go` (two projects never share a
password; the shape is long enough and free of lookalikes; an explicit
password is honoured; and main.go carries no password, no `envOr` fallback
and no logging of it) and `TestSeedAdminRefusesAWeakPassword`. All
verified to fail against the pre-fix files. Also generated a real project
end to end and built it, to be sure the template still compiles.


### [x] 8. The last superadmin can demote themselves, permanently

`admin/handlers_users.go:150` guards only
`self.Role.IsAdmin() && !form.Role.IsAdmin()` — superadmin → admin passes
both sides. Only a superadmin can *assign* superadmin
(`parseUserForm:456`), and `SeedAdmin` is a no-op once any user exists
(`cms.go:961`). So the site loses snippets, the Pages section, masquerade
and the lock override with no in-app recovery — only a direct DB edit.

**Fixed** by enforcing the invariant rather than patching the one path.
The reachable bug was self-demotion, but writing the guard as "a
superadmin may not set their own role to admin" would have left the
roundabout routes open, so `lastActiveSuperadmin` asks the question the
invariant actually cares about: would this change leave nobody?

That turned out to matter. A superadmin can masquerade as *another*
superadmin, demote the account they came from, and then demote the one
they are wearing — every step of which is one superadmin managing
another, which every role rule allows. There is a test for it, and it
fails against the pre-fix code.

Backed by `auth.Store.CountOtherActiveSuperadmins`, which counts *active*
ones: an account that cannot log in is not the somebody left behind.
Enforced on update and, redundantly, on delete — deleting a superadmin
currently takes another superadmin, who is therefore still there, but that
is two other rules holding the invariant up rather than the invariant
itself. A failed count reads as "yes, they are the last", since refusing a
role change on a database that is not answering is the recoverable way to
be wrong.

The message lands on whichever field caused it and only where nothing more
specific has already spoken — deactivating your own account is refused for
its own reason, and that reason reads better.

Tests: `admin/last_superadmin_db_test.go` — self-demotion refused and *not
applied*; the masquerade route; a deactivated superadmin not counting; and
`TestSuperadminCanStepDownOnceAnotherExists`, which pins the other
direction so this does not become "superadmins are frozen". Plus a direct
test of the count query. The three blocking tests fail against the pre-fix
code; the step-down test passes both ways, which is what makes it worth
having.

Not addressed: a site *already* at zero superadmins still needs a database
edit. A recovery hatch (an env var that promotes an account) would be a
new feature and a new risk, so it is deliberately not here.


### [x] 9. Sitemap cache is keyed on the attacker-controlled `Host`

`sitemap.go:67` — `c.sitemapBase != base` invalidates the cache, so one
unauthenticated request per distinct `Host` header forces a full rebuild
(up to 50k rows plus XML marshal) *and* evicts the cached copy. Cheap to
send, expensive to serve.

**Fixed** by splitting the cache along the line the cost actually falls
on. The rows — a query over every page on the site — do not depend on the
base at all, so they are now cached on their own and are simply out of
reach: vary the Host all you like and the query still runs once per TTL.
What stays host-dependent is turning rows into XML, which is arithmetic on
data already in hand. The rendered document is still cached too, for the
ordinary case where every request names the same base.

Rejected alternatives, since they look tempting: *latching the first base
seen* would let a spoofed first request fix the wrong hostname for the
life of the process, and a sitemap full of another host's URLs is ignored
by crawlers — a self-inflicted SEO outage, worse than the thrash.
*Refusing unfamiliar bases* would break a legitimate install that has not
set `SiteURL`, which the docs still permit.

The rebuild also now happens under the mutex, so concurrent misses queue
behind one build instead of each starting their own — the other half of
not letting a client multiply work by asking twice.

With `SiteURL` set (which #1 made the production norm) the base is
constant and every request is a plain cache hit, so production is
unaffected either way.

Tests: `TestSitemapRowsAreNotRebuiltPerHost` drives the attack — five
novel hosts after a page is published mid-flight — and proves the rows
stayed put *behaviourally* (the new page must not appear) as well as by
the timestamp, while checking each host still gets its own URLs, since the
fix is about what is recomputed and not about serving one host's URLs to
another. `TestSitemapWithSiteURLIgnoresTheRequestHost` pins the
configured-base case. Verified to fail against the pre-fix keying.

One test helper corrected: `expireSitemapCache` cleared only the rendered
document, so after the split it would have left the next request
re-rendering rows read before the test changed anything — the exact
staleness the surrounding test was checking for. It now drops both halves.


### [x] 10. CAPTCHA fail-open is wider than the comment claims

`captcha/captcha.go:154`. The comment says it fails open on outage, but
`Verify` also returns an error on a JSON decode failure, and both call
sites (`handlers_auth.go:54`, `handlers_reset.go:84`) treat any error as
"skip the check". A 200 with a malformed body is a received response, not
an unreachable server, and arguably should be a rejection.

**Fixed**, and the investigation turned up something worse than the filed
issue. A body that is JSON but *not Cap's* — `{"error":"unknown site
key"}`, or `null` — decoded cleanly into `struct{Success bool}` and came
back as `Success=false`, which the callers read as a rejection. So the
same misconfiguration failed **closed** when the wrong thing on the other
end spoke JSON and **open** when it served an HTML error page. A wrong
site key locked every admin out of the site; a wrong URL silently switched
the CAPTCHA off. Confirmed by test against the pre-fix code.

`Verify` now decodes into a `*bool`, so "said false" and "did not say"
stay apart, and returns typed errors: `ErrUnavailable` (transport failure
or 5xx — the outage the fail-open path exists for) and `ErrBadResponse`
(something answered, but not with a verdict). Every error now means the
same thing — no verdict — which is what the doc comment always claimed and
the code did not do. The response body is also bounded, so a server that
keeps writing cannot hold the goroutine with it.

The callers keep failing open, deliberately: the alternative locks every
account out of a site over a dependency that is not the site, and #3's
per-account throttle means guessing is now genuinely capped without it.
That decision moved into one shared `captchaNoVerdict` instead of being
made twice, and it logs the two kinds differently — `ErrBadResponse` at
Error, saying plainly that the challenge is not being enforced, because
that one never fixes itself.

Tests: `TestVerifyNoVerdictKinds` (eight shapes, each asserted to match
one error kind and *not* the other), `TestVerifyVerdictsAreNotErrors`,
`TestVerifyBoundsTheResponseBody`, and
`admin/captcha_verdict_db_test.go` for the call-site behaviour. All
verified against the pre-fix code.

One test of mine was thrown away and rewritten. The first version drove
the login form with the honeypot filled, to stand in for a credential
check without a database — but the honeypot is checked *before* the
CAPTCHA, so it short-circuited and never consulted Cap at all. It passed
against the broken code, which is how it was caught. The replacement runs
against a real database and asserts a valid user actually reaches the
dashboard.


---

## Robustness and efficiency

### [x] 11. `currentUser` is unmemoized — 47 call sites, 2 queries each

`admin/middleware.go:43` does `GetByID` + `loadPermissions` on every call,
with no request-scoped cache. A `GET /users` runs it in `requireUser`,
`requirePerm`, the handler, `newTemplateData`, and `canUseMedia` — five
calls, ten queries, for one page. (The users list itself is fine;
`canManageUser` was correctly hoisted out of the row loop.) One
`context.WithValue` in `requireUser` would collapse this.

**Fixed** with a request-scoped memo installed by `withUserCache`, mounted
at the top of the admin chain — above `csrf`, so the routes outside
`requireUser` (login, the reset forms) get one too. `GET /users` now costs
one lookup instead of five.

Two details the obvious version would have got wrong. The memo records
*nil* as an answer, because a session naming a deleted or deactivated
account is the case where re-asking is most wasteful — every check in the
chain asks, and every one is told nothing. And it is keyed by the session
user id, re-read from the (in-memory) session on each call, so a request
that changes who it is signed in as does not keep the old answer.
Masquerade, login and logout all redirect rather than render, so nothing
needs that today; the memo should not be the reason that stays true.

Tests: `admin/user_cache_db_test.go`. Queries cannot be counted from a
test — `Deps.Users` is a concrete `*auth.Store`, so nothing can be wrapped
around it — so the memo is tested by what it *means*: change the stored
row mid-request and see whether the same request notices. Also that it is
per request and not process-wide (a deactivated account must not stay
signed in), that a nil answer is recorded, that a session swap is
followed, and — separately — that the middleware is actually mounted on
the real chain, which a unit test of `currentUser` alone would never
catch. Verified against both failure modes: memo removed, and middleware
un-mounted.

### [x] 12. Site settings are read twice per public page render, and can disagree

`siteFlags` (`cms.go:1269`) exists specifically so the mode isn't a query
per request — then `servePage:1406` calls `c.content.SiteSettings()`
directly and uncached. Two consequences: the cache buys nothing on the
page path, and within one response `site.dev` from the 5s cache can set
`X-Robots-Tag: noindex` while the fresh read renders no `<meta robots>`.
`siteFacts` is a subset of `SiteSettings` — cache the whole struct once.

**Fixed.** `siteFlags`/`siteFacts` are gone; `siteSettings` caches
`content.SiteSettings` itself and everything reads through it —
`developmentMode`, `SiteLocked`, `robotsBody`, `Pages`, `servePage` and
`serveSearch`. The duplicate type went with it, which was the other half
of the finding: a summary struct whose fields were a subset of the record
it summarised.

The failure path improved on the way: a read that fails now falls back to
the last good copy rather than to a partially-populated struct. The
previous code returned `out, rows.Err()` from `SiteSettings`, and the
callers that logged and carried on were carrying on with whatever half of
the settings had been scanned before the error.

Tests: `TestPageHeadersAndBodyAgreeOnTheSiteMode` stands inside the window
— it changes the stored mode *without* expiring the cache, so the two
readings would have come from different moments — and asserts the header
and the `<meta robots>` tag agree either way. Verified to fail against the
pre-fix code with exactly the reported symptom: "header says noindex =
true, body carries the meta tag = false".

The first version of that test was wrong and passed for the wrong reason:
it used the shared seed template, which never calls `{{cmsHead}}`, so the
meta tag could not appear whatever the mode was. It now builds a template
that emits one.

### [x] 13. All session I/O runs on `context.Background()`

`internal/sessionstore/sessionstore.go:40,55,64` and
`internal/redisstore/redisstore.go:32,48,53` implement scs's non-context
`Store`. scs v2.9 offers `CtxStore` (`FindCtx`/`CommitCtx`/`DeleteCtx`)
and prefers it when present. As written, a session read has no deadline
and ignores client disconnects — under DB stress those queries pile up
unbounded.

**Fixed**, but not by passing the context through everywhere, which is
what "implement CtxStore" sounds like and would have been a regression in
one place.

Reads take the request's context unchanged: a lookup for a visitor who is
no longer there stops with them, which is the pile-up the finding is
about.

Writes deliberately do not. A session write happens because something has
already been decided — a login granted, a session destroyed on logout, a
token renewed against fixation — and a client that hangs up between the
decision and the write must not undo it. A logout that did not delete the
session because the browser went away leaves a session that still works,
and passing the request context straight through would have introduced
exactly that. So writes drop the cancellation and take a deadline instead
(`sessiondata.WriteContext`): they survive the disconnect and still cannot
run forever, where before they could.

The context-less three remain, delegating, for anyone holding a store
through the narrower `scs.Store`.

Both stores now carry `var _ scs.CtxStore = (*Store)(nil)`. That is not
decoration: scs picks the `*Ctx` methods by asserting on each signature
one at a time, so a method whose signature drifted would be silently
skipped and the store would go back to `context.Background()` with nothing
to say it had. The assertion is what notices.

Tests: `TestFindCtxHonoursCancellation` and
`TestWritesOutliveACancelledRequest` in both stores, plus
`TestPlainMethodsStillWork` for the narrower interface. Verified against
an implementation that has the `*Ctx` methods but ignores the context —
the pre-fix behaviour wearing the new interface, which the compile-time
assertion alone would not catch.

### [x] 14. No `CMS.Close()`

`sessionstore.New` starts an hourly cleanup goroutine and exposes
`StopCleanup`, but `cms.New` (`cms.go:706`) neither keeps a reference nor
offers a shutdown method. Same for the Redis client. Every `cms.New`
leaks a goroutine — invisible in production, but it accumulates in tests
and in hosts that rebuild the CMS.

**Fixed.** `New` collects what it starts into `closers` as it creates it,
and `Close` runs them: the session store's sweep, and the Redis pool when
sessions live there. Idempotent via `sync.Once`, since a shutdown path
reachable from both a defer and a signal handler is reachable twice.
`Config.DB` is deliberately untouched — the host opened it and may be
using it for its own tables.

`sessionstore.StopCleanup` was itself a latent panic: it closed the stop
channel unguarded, so a second call would have crashed. It now uses a
`sync.Once` and *waits* for the goroutine to return, which is what makes
the test deterministic rather than a sleep. `defer c.Close()` added to the
scaffold and both examples, and to the host-integration checklist.

Tests: `TestCloseStopsTheSessionSweep` (returns, and twice is not a
panic), `TestCloseLeavesTheHostsDatabaseOpen` (a query on the host's pool
still works afterwards), and `TestStopCleanupEndsTheGoroutine` /
`TestStopCleanupWithoutACleanupLoop` in the session store.

### [x] 15. Error classification by substring match

`tailwind.go:313` — `strings.Contains(err.Error(), "no rows")` instead of
`errors.Is(err, sql.ErrNoRows)`. If it ever stops matching,
`loadContentCSS` returns an error, `buildOnce` bails, and the stylesheet
silently stops rebuilding. Same pattern at `admin/handlers_media.go:228-229`
and `handlers_api.go:1228` (`"decoding image"`, `"parsing svg"`) — those
should be sentinel errors from the `media` package.

**Fixed.** `tailwind.go` uses `errors.Is(err, sql.ErrNoRows)`. The media
package gained `ErrUndecodable` — a file whose type is one it handles but
whose bytes will not read as it — wrapped around both the image-decode and
SVG-parse failures, so the detail survives for the log while the kind is
findable with `errors.Is`. It is separate from `ErrUnsupportedType`
because the two are different news for whoever uploaded the file: "we
don't take those" against "that one is damaged".

No `strings.Contains(err.Error(), …)` remains in non-test code.

Tests: `TestUndecodableFilesAreDistinguishable` — a truncated PNG and a
malformed SVG are `ErrUndecodable` and *not* `ErrUnsupportedType`, an
unhandled MIME type is the reverse, and the wrapped detail is still in the
message. Verified to fail against the unwrapped errors.

### [x] 16. Upload `MaxBytesReader` calls are dead on the form path

`handlers_media.go:206`, `handlers_api.go:1196,1258` set
`r.Body = http.MaxBytesReader(...)` — but `readToken`
(`middleware.go:294`) has already called `ParseMultipartForm` for
form-posted requests, and its own comment says any later limit "is dead
code that reads as if it works." These are live only for the JS uploader's
header path. Either drop them or comment why they're conditional; as
written they read as the enforcement and aren't.

**Fixed** by making the ceiling uniform rather than by deleting the
readers, which would have been a regression: on the header path they were
the *only* bound. `readToken` now applies `http.MaxBytesReader` before it
looks for the header, so the limit is a property of the request rather
than of how the token happened to arrive, and the handler-level readers
are genuinely redundant and gone.

`apiMediaSetPoster`'s narrower 8 MB cap moved from `r.Body` onto the
multipart part (`io.LimitReader`), which bounds it on both paths; the
duplicated literal became `maxPosterBytes`. Oversized *uploads* (as
opposed to oversized requests) were already caught by the manager from the
multipart header's declared size, on either path.

Tests: `TestBodyCeilingAppliesToBothCSRFPaths` drives a host section that
reads its own body, so the limit is observed directly rather than inferred
from a status code. Verified to fail with the ceiling back on the form
path only: "the handler read 16384 bytes of a 16384-byte body with no
error".

The first version of that test asserted on status codes at `/login`, and
passed against the broken code — the nil user store panicked before any
status distinguished the two cases. Replaced.

### [x] 17. Two env parsers silently ignore bad values

`CMS_MEDIA_MAX_VIDEO_MB` and `CMS_MEDIA_WEBP_QUALITY` (`env.go:150,158`)
check only the parse error; negative/out-of-range values pass through and
are then discarded by `SetMaxVideoBytes`/`SetWebPQuality`. Every other
variable in that file rejects out-of-range values with a clear message.

**Fixed**: both are range-checked where they are read, like every other
variable in the file. A quality of 3 or a video cap of -1 was previously
accepted, discarded by the setter, and replaced by the default — so the
site ran on a number nobody chose and nothing said so.

Tests: `TestConfigFromEnvMediaTuningRanges`, ten cases across both
variables. Verified to fail against the parse-only checks.

---

## Duplication

### [x] 18. Page-write logic is forked between `page.go` and `post.go`

`content.InsertPost` (`post.go:173`) reimplements `Insert`'s
(`page.go:334`) three statements — `cms_pages`, `cms_page_drafts`,
`cms_page_meta` — and `UpdatePost` does the same for `Update`. The
divergence is already there: `Insert` writes `visibility` explicitly,
`InsertPost` omits it and relies on the column default. This is the
duplication most likely to bite; a shared `insertPageRows(tx, …)` would
fix it.

**Fixed**, but only the parts that were genuinely the same — which turned
out to be most, not all, of it.

`insertPage` and `saveStagedPage` in `page.go` now write the rows both
paths share: the page row, the working copy, and the per-locale metadata.
`InsertPost` and `UpdatePost` call them. The insert also writes visibility
explicitly now instead of leaning on the column default, which is what had
drifted; no caller sets a non-public visibility on a new post, so the
behaviour is unchanged and the row simply says what it means.

The update path keeps one difference, and it is not an oversight:
`UpdatePost` must *not* write visibility. A post carries no visibility
control of its own, so `parsePostMeta` builds a fresh `&content.Post{}`
with the field left zero — and `orPublic()` turns a zero into "public".
Unifying the two updates would therefore silently re-publish a backing
page somebody had made private through the editor, which `apiSetVisibility`
can reach. That difference is now stated in a comment and pinned by a test
instead of reading like a missed line.

Tests: `TestPostBackingPageIsPublicLikeAnyPage` and
`TestUpdatePostKeepsAPrivateBackingPagePrivate`.

Two things worth recording about getting here. First, I transposed
`template_name` and `visibility` while writing the shared insert; the
existing suite caught it immediately on all three engines via the
visibility CHECK constraint, which I confirmed by reintroducing the swap
on purpose. Second, the first version of the visibility test read the post
back before re-saving it — which carries the private setting along, so it
passed against a deliberately unified `UpdatePost` and proved nothing. It
now builds the Post the way the admin form does, and fails against that
unification on every engine.

### [x] 19. `requirePerm` is a pure alias

`admin/middleware.go:149-151` — `return s.requireAnyPerm(p)`. Either drop
it and call `requireAnyPerm` at the ~4 sites, or keep it and delete the
doc comment that describes it as a distinct thing.

**Fixed** by dropping it. Five call sites (four in `admin.go`, one in
`sections.go`) now call `requireAnyPerm` directly, and its doc says what
it does rather than deferring to a function that no longer exists.
Naming one permission was never a different operation from naming two.

### [x] 20. `escapeLike` vs `likeEscaper`

`content/search.go:452` and `auth/user.go:166` — identical `\`/`%`/`_`
replacer, written twice. The `content` copy also allocates a fresh
`strings.NewReplacer` on every call instead of using a package-level var.

**Fixed**: one `sqldb.EscapeLike`, since the rule is the engines' rather
than any one table's, and both stores already import `sqldb`.

There were **three** copies, not two — `media/manager.go` had an
`ilikeEscaper` the original review missed, which is a fair illustration of
how this kind of duplicate spreads. All three call sites now share one
package-level replacer, so nothing rebuilds it per call.

Tests: `internal/sqldb/escapelike_test.go`, including that the backslash
is escaped first (or an escape the caller typed would become an escape of
ours) and that nothing else is touched — this is a LIKE escaper, not a
sanitiser, and the value still reaches the driver as a bound parameter.

### [x] 21. `absoluteAdminURL` reimplements `requestBaseURL`

`admin/handlers_reset.go:228-233` vs `posts.go:187` — same `r.TLS` /
`X-Forwarded-Proto` / `r.Host` logic in two packages. The admin already
receives `Deps.SiteBaseURL`; the fallback branch is the duplicate, and
it's the branch that carries finding #1.

**Resolved by #1**, and deliberately not merged further. `absoluteAdminURL`
became `emailBaseURL`, which now requires a loopback host and ignores
`X-Forwarded-Proto` — while `requestBaseURL` trusts it. What is left in
common is three lines of scheme-picking, and the difference between them
*is* the security distinction #1 drew. Folding them back together would
re-couple exactly what that fix separated, so this one is closed as
already-addressed rather than by writing more code.

### [x] 22. Page/post handler triples

`handlers_pages.go:587-643` vs `handlers_posts.go:183-232` —
`pageDelete`/`pageDiscard`/`pageUnpublish` and their post equivalents are
structurally identical, differing only in the loader, the noun in the
flash, and the redirect base. `handlers_versions.go` already shows the
shared shape works (`renderVersions`, `restoreVersion` take a
`base string`). Lower value than #18 — the messages genuinely differ —
but it's the largest remaining copy-paste in the admin.

**Fixed** with `finishContentAction` and `refuse`, which the six handlers
now end in. The messages stay whole translated literals passed in, because
they differ by more than a noun and building them from parts would put
them beyond the translation catalogue.

The value is not the lines saved. It is `contentChanged`: leaving it out
of a handler breaks nothing visible — the site keeps working and classes
typed into content quietly stop being compiled, surfacing much later as a
style that "just doesn't apply" — so the notification belongs in the
shared path rather than in six places that each have to remember it.

Tests: `TestContentActionsNotifyContentChanged`, which had no coverage
at all before (nothing in the admin tests referenced `ContentChanged`).
Verified by removing the notification from the shared tail: all three
actions fail.

---

## Suggested order

1. #1 (reset-link poisoning)
2. #2 (SVG SMIL)
3. #4 / #5 / #7 together — "the generated site isn't production-safe by default"
4. #11 and #12 — cheapest performance wins
5. #18 — the duplication worth paying down
