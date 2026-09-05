# Users, roles & permissions

Who can do what: the role model, the dashboard, and the account features — bot protection, password resets, and two-factor login.

## Roles & permissions

Accounts have one of three **roles**, which encode trust:

- **editor** — works on content, gated by per-user permissions (below).
- **admin** — everything: all permissions implicitly, plus site-wide and
  per-page CSS/JS (written into pages unsanitized), the custom-code block
  library, and user management without restriction.
- **superadmin** — admin plus snippet management (snippets are raw HTML
  injected into every editor), unlisted page templates in the new-page
  dialog, the development/production switch (whether the site may be
  indexed at all), and the admin panel's Pages section (everyone
  else works on pages in place on the public site, where every page
  feature is available; the admin list is the superadmin's index of
  pages that aren't linked anywhere).

What an *editor* may work on is a set of per-user **permissions**,
toggled on their page under Users:

| Permission | Grants |
|---|---|
| Blog posts | the blog feed: creating, editing, publishing blog posts |
| News | the news feed, the same way |
| Pages, menus & site settings | site pages, navigation menus, and the non-code site settings, all through the in-place editor (the site mode stays superadmin-only) |
| User management | managing *editor* accounts (see below) |

Everything follows from the grant: nav entries and dashboard cards the
user can't act on don't render, the routes 403 regardless, and on the
public site the in-place editor appears only on pages the user may edit
— a blogs-only editor gets the editor (and sees drafts) on `blog/…`
pages and the ordinary published render everywhere else. The media
library stays open to every logged-in user; content work needs it.

A migration grants existing editors the three content permissions, so
an upgrade changes nothing until you start unticking boxes.

**User management without the admin role is deliberately bounded.** An
editor with that grant manages editor accounts only: admin accounts are
out of reach entirely (including their passwords and two-factor
resets), the role select offers only "editor", and they can neither
grant nor revoke a permission they don't hold themselves. Escalation by
way of the users page is a dead end.

Deployments can declare **custom permissions** for functionality they
gate themselves — either implicitly through an admin section's
`Permission` field (see [Custom admin pages](host-integration.md#custom-admin-pages)), or
standalone:

```go
Permissions: []cms.PermissionDef{
    {Key: "vehicles", Label: "Manage vehicles"},
},
```

Each declared permission becomes a checkbox on the user form; grants
live in the `cms_user_permissions` table. Check them in your own
handlers with `auth.User.Can` — inside an admin section that's
`admin.UserFrom(r).Can("vehicles")` (admin roles always pass). Keys are
lowercase identifiers (`[a-z][a-z0-9_-]*`, max 64 chars); the built-in
keys `blogs`, `news`, `pages`, and `users` are reserved.

A permission can also be made to **bind admins**: declare it (or its
section) with `AdminsNeedGrant: true` and the admin role stops holding
it implicitly — the checkbox must be ticked for an admin exactly as for
an editor, and only superadmins hold it regardless. Check these in your
own handlers with `auth.User.HasGrant` rather than `Can`. Every
declaration of one key must agree on the flag; `cms.New` refuses a
mismatch.

## The dashboard

The admin's landing page is built for the people who use it daily:

- **Host section cards come first** — whatever the deployment registered
  with `AdminSection.Dashboard` (above), visible by the section's own
  permission rules. For most editors these cards *are* the dashboard.
- **The built-in cards — Pages, Snippets, Users, Media — are
  superadmin-only.** They are site plumbing; editors and admins do their
  content work in place on the public site. **Blog & News** renders for
  anyone holding a blogs or news grant.
- **A traffic chart** shows the public site's page views for each of the
  last seven days (UTC), for every logged-in user, with the week's five
  most-viewed pages listed beside it.

The traffic numbers come from the CMS itself: serving a page to an
anonymous visitor upserts a per-day, per-path counter in
`cms_page_views` — no cookies, no IPs, no user agents stored, so there
is nothing here a privacy policy needs a section for. Logged-in CMS
users aren't counted (staff aren't traffic), and neither are crawlers
that identify themselves. `Migrate` prunes counters older than ninety
days on every startup.

## Bot protection

The public site is read-only (GET/HEAD only), so the bot-facing surface
is the admin login form — and the forgot-password form, when a Mailer is
configured (see the next section). Three layers protect both:

- **Login throttling** (always on): five failed attempts per email+IP in
  fifteen minutes, then 429 responses until the window passes.
- **Honeypot** (always on): a visually hidden form field; anything that
  fills it gets the ordinary wrong-password error and a throttle strike.
- **CAPTCHA** (opt-in): a proof-of-work challenge verified against a
  self-hosted [Cap](https://capjs.js.org) server — no third-party
  service, no tracking, and the widget script is served by your own Cap
  instance rather than a CDN.

To enable the CAPTCHA, run Cap (docker image `tiago2/cap`, see
`examples/basic/docker-compose.yml`), open its dashboard, log in with
the container's `ADMIN_KEY`, create a site key, and configure:

> **Pin the widget version.** Set `WIDGET_VERSION` and `WASM_VERSION` on the
> Cap container rather than leaving them at `latest`. The widget is a browser
> dependency of the login page: on `latest`, an upstream release can change
> its API or its CSP requirements under a deployment that has not changed at
> all. The compose file pins known-good versions; treat a bump like any other
> dependency upgrade and log in against it before committing.
>
> The login page's CSP also carries `'unsafe-eval'`, because Cap's
> instrumentation challenge calls `eval()`. It is scoped to that one page —
> every other admin page gets a strict `default-src 'self'` policy. Turning
> instrumentation off for the site key in the Cap dashboard removes the need
> for it, at the cost of the anti-automation layer.

```go
c, err := cms.New(cms.Config{
    // ...
    Captcha: &cms.CaptchaConfig{
        URL:     "https://cap.example.com", // browser-facing Cap server
        SiteKey: "your-site-key",
        Secret:  "your-secret",
        // InternalURL: "http://cap:3000", // optional: server-to-server
        //                                 // address, e.g. inside Docker
        // Visible: true,                  // optional: show Cap's checkbox
        //                                 // widget instead of solving the
        //                                 // challenge invisibly (default)
    },
})
```

With `Captcha` set, the login page solves the challenge invisibly in the
background (Cap's programmatic mode) — users never see a CAPTCHA. Set
`Visible: true` to show Cap's interactive checkbox widget instead. Either
way the admin CSP is extended to admit exactly the Cap origin, and the
login handler verifies the submitted token server-side before checking
credentials. If
the Cap server rejects the token, the login fails; if the Cap server is
*unreachable*, the login proceeds with a logged warning — an outage of
the CAPTCHA backend shouldn't lock admins out, and the throttle still
applies. Host applications can reuse the verification client
(`captcha.New`, `Client.Verify`) for their own forms.

## Password resets ("forgot password")

The login page can offer a self-service reset: ask for a link, get an
email, follow it, set a new password. The CMS owns the whole flow — the
token table, the two pages, the throttling, and the wording of the email
— and the host supplies exactly one thing: delivery.

```go
// Satisfy the one-method interface with whatever your application
// already sends mail through:
type cmsMailer struct{ m *yourMailer }

func (a cmsMailer) Send(ctx context.Context, to, subject, text, html string) error {
    return a.m.Deliver(ctx, to, subject, text, html)
}

c, err := cms.New(cms.Config{
    // ...
    Mailer: cmsMailer{yourAppMailer},
})
```

That split is deliberate. Delivery policy — SMTP or an API, which From
address, a development mail sink — already lives in the host, and the
CMS should not duplicate it. The message content goes the other way:
the CMS authors the email so every deployment sends the same
carefully-worded thing, in particular the part that never confirms
whether an address has an account.

**Nil means off.** With no `Mailer` configured, the login page shows no
"Forgot your password?" link and the reset routes answer 404. A reset
form that could never send its email would *look* broken; absent, the
feature is honestly off. A host that wants the flow without real
delivery (development, tests) can pass a Mailer that logs.

What the flow does, so you don't have to re-derive it from the code:

- **Tokens are single-use and expire after an hour** (`auth.ResetTTL`).
  Asking again revokes the earlier link, so at most one works at a time.
  The database stores only a SHA-256 of the token — the email holds the
  only usable copy, so a leaked backup or a curious query replays
  nothing.
- **No account oracle.** Every address gets the same confirmation page,
  and the email (when there is one) is sent in the background so known
  addresses are not measurably slower than unknown ones. Deactivated
  accounts get the same page and no email.
- **The same defenses as the login form**: its own throttle (five
  requests per email+IP per fifteen minutes — this endpoint makes the
  server send email, which is worth as much to a spammer as a password
  guess is to a thief), the honeypot, and the CAPTCHA when one is
  configured.
- **A typo doesn't burn the link.** Password validation failures
  re-render the form with the token intact; the token is only consumed
  once a valid new password is installed.
- Both pages and the email are translated when the site has a French
  locale, like the rest of the admin.

The email link is built from `Config.SiteURL` when set, otherwise from
the request's own scheme and host — the same rule as every other
absolute link the CMS mints. If the admin is reached behind a proxy that
rewrites `Host`, set `SiteURL`.

`examples/wheels` wires this up for real (see `adminMailer` in its
`mail.go`): the adapter is five lines around the mailer the site already
had, and it only sets `Config.Mailer` when mail is actually configured —
so a fresh checkout without SMTP credentials gets the honest absent
state rather than emails that vanish into a log.

## Account settings and two-factor login

Every logged-in user has an account page at `/admin/settings` (their name
in the sidebar links to it) with three things on it: editing their own
name and email, changing their own password — behind the current one, so
a walked-away session can't quietly take over the account — and turning
**two-factor login** on or off.

Email edits get the same validation as the admin's user form (a
well-formed address that no other account holds) and additionally
require the current password, because the address is the login
identifier and where reset links go. A name change alone needs no
password. Role and active status are deliberately not on this page —
nobody adjusts their own powers outside the admin-only `/admin/users`.

Two-factor uses ordinary TOTP authenticator apps (Google Authenticator,
1Password, Authy, …): the settings page shows a QR code and a
manual-entry key, and enrollment only saves once a live code from the
app confirms it — nobody locks themselves out by enabling it with an app
that never scanned the code. From then on, the password step of login
parks the user at a 6-digit code challenge; the session only exists
after the code passes. Nothing to configure: the feature is per-user and
always offered.

What the flow does, so you don't have to re-derive it from the code:

- **Codes are single-use.** Each accepted code's 30-second time step is
  recorded (`totp_last_step`), and a login only succeeds by moving it
  forward — a shoulder-surfed or phished code replays nothing. One step
  of clock skew either side is accepted, like everything else that
  speaks TOTP.
- **The challenge is throttled** like the login form (five wrong codes
  per account+IP per fifteen minutes), and it expires: a correct
  password opens a five-minute window to produce the code, then the
  half-login goes stale.
- **Turning it off requires the password** — a borrowed session alone
  can't strip the second factor.
- **Lost phone?** An admin editing the user (`/admin/users/…`) gets a
  "Reset two-factor authentication" checkbox; the user logs in with
  just their password and can re-enroll.
