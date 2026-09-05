# Going to production

Keeping a site out of search engines until it is ready, robots.txt and sitemaps, and closing the whole site behind the lock.

## Development & production: keeping a site out of search

The same dialog carries a **Site mode** switch, which only superadmins
see. A site in **development** is built and browsable but asks search
engines to leave it alone; switching it to **production** is what makes
it findable. `SeedAdmin` starts a brand-new site in development, so a
site is never indexed while it is still being written. Sites that
predate the setting stay in production — an upgrade never quietly pulls
a live site out of search.

In development the CMS:

- sends `X-Robots-Tag: noindex, nofollow` on **every** public response —
  pages, RSS feeds, and media. The header is what covers the files no
  `<meta>` tag can reach: search engines index a PDF or an image on its
  own, and the media proxy serves both.
- emits `<meta name="robots" content="noindex, nofollow">` from
  `{{cmsHead}}`.
- serves `/robots.txt` with `Disallow: /`.

In production it does none of those things, and — unless you have written
a robots.txt in the site settings (below) — does not claim `/robots.txt`
at all. If your app serves its own (with a `Sitemap:` line, or rules for
a particular crawler), it keeps serving it the moment the site goes live;
the CMS's copy is only there while the site is hidden.

Two things worth knowing:

**This hides the site; it does not protect it.** Everything is still
served to anyone with the address, and a crawler that ignores the rules
is not stopped by them. If an unfinished site must not be *reachable*,
that is HTTP auth, an IP allowlist, or not pointing a public name at it —
none of which the CMS does for you.

**Development mode is for a site that was never indexed.** `Disallow`
keeps crawlers out, and a crawler that never fetches a page never sees
the `noindex` on it. That is the right trade before launch, when there is
nothing in anyone's index yet. It is the wrong tool for pulling a site
that *has* been live back out of search results: for that the pages have
to stay crawlable so the `noindex` can be read, which means removing the
URLs through the search engine's own tools rather than flipping this
switch.

Everywhere else the mode stays visible: while a site is in development
the admin sidebar carries a **Development** stamp under the brand, on
every page, because the failure this feature invites is a finished site
nobody remembered to switch over.

### A robots.txt for the live site

Under the mode switch — and, like it, superadmin-only — the site settings
dialog carries a **robots.txt** box. Whatever you write there is served
verbatim at `/robots.txt` once the site is in production:

```
User-agent: *
Disallow: /private

Sitemap: https://example.com/sitemap.xml
```

On a site that has never stored one, the box opens on a working starting
point rather than empty — crawl everything except the admin, at whatever
`Config.AdminPath` you mounted it on:

```
User-agent: *
Disallow: /admin/
```

Nothing is stored until you save, and the note under the box says so: the
save is what takes `/robots.txt` over from the host app. Clear the box and
save to hand it back.

Three rules govern it, and they are worth stating plainly:

- **Empty means the CMS serves nothing there.** That is the default and
  the behaviour every existing site keeps: the path stays the host app's,
  and an app already serving its own file is unaffected by this feature
  existing. The suggested text above is only ever a suggestion — it takes
  a save to become real.
- **Development ignores it.** A hidden site serves its own `Disallow: /`
  no matter what is stored, because a file written for the live site
  would otherwise invite crawlers into an unfinished one. The box says so
  while the site is in development.
- **Only superadmins may edit it.** Admins and editors see the dialog and
  save the rest of it normally; their save carries the stored file
  through untouched, the same way it carries the mode.

Responses are sent `Cache-Control: no-store`, so an edit is live at once
as far as any proxy is concerned. Crawlers cache `robots.txt` on their
own schedule regardless — Google for about a day — so a change takes
effect on their next fetch, not yours.

This is a text box, not a validator: the CMS caps the length and
normalizes line endings, and otherwise serves what you typed. A
`Disallow` that hides a page from search does not make it unreachable —
that is the same caveat as development mode, and worth re-reading above.

### A sitemap

Above the robots.txt box, and superadmin-only in the same way, is
**Publish a sitemap at /sitemap.xml**. With it on, the CMS generates a
sitemap of every page it serves:

```xml
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/</loc>
    <lastmod>2026-08-15T12:04:11Z</lastmod>
  </url>
  <url>
    <loc>https://example.com/about</loc>
    <lastmod>2026-07-02T09:30:00Z</lastmod>
  </url>
</urlset>
```

**What it lists** is every page that is published *and* publicly visible
— posts included, since a post is a page. Drafts and private pages are
left out, which means the sitemap says exactly what an anonymous visitor
could reach anyway.

**`lastmod` is the live page's date, not the editor's.** It moves when a
page is published, unpublished, renamed, or has its visibility changed,
and stays put while someone works on a draft — draft edits are stored
separately from the page row. So editing all afternoon without publishing
does not tell search engines the page changed, which is correct: it
hasn't.

**Multi-locale sites list every language**, and each URL carries the
`hreflang` alternates — including `x-default` — that `{{cmsHead}}` already
emits in the page head, so the two agree about which URLs exist:

```xml
<url>
  <loc>https://example.com/about</loc>
  <xhtml:link rel="alternate" hreflang="en" href="https://example.com/about"/>
  <xhtml:link rel="alternate" hreflang="fr" href="https://example.com/fr/about"/>
  <xhtml:link rel="alternate" hreflang="x-default" href="https://example.com/about"/>
</url>
```

**Turning it on advertises it.** If you have written a robots.txt, a
`Sitemap:` line pointing at it is added to what gets served — unless your
file already names a sitemap, in which case yours is left alone. An empty
robots.txt box stays empty: the sitemap does not make the CMS start
claiming `/robots.txt`.

Three more things worth knowing:

- **New sites get it; upgrades don't.** `SeedAdmin` turns it on for a
  brand-new site, exactly as it starts one in development mode. An
  existing site is left alone — if your app already serves its own
  `/sitemap.xml`, an upgrade must not quietly take the address over.
  Turn it on in the dialog when you want the CMS's.
- **A site in development publishes none.** It is asking not to be
  crawled; handing out a list of every URL it has is the opposite of
  that. The switch stays where you left it and takes effect at the
  production flip.
- **The document is cached for five minutes.** A page published a moment
  ago may not appear until then. That is a bound on cost, not a freshness
  promise — crawlers refetch on their own far slower schedule. The URLs
  come from `Config.SiteURL` when you set one, and otherwise from the
  requesting host, so an install reached by several names answers each
  with its own.

Past 50,000 URLs — pages × locales — the extra pages are left out and a
warning is logged. Splitting into a sitemap index is the fix, and does
not exist yet.

## Closing the site: the lock

Development mode asks crawlers to stay away. The **lock** turns everyone
away — it is the switch for the afternoon a site has to be *off*: a bad
import, a price list that went out wrong, a rebuild mid-flight.

While it is on:

- every public address answers `503 Service Unavailable` with a
  `Retry-After` and `X-Robots-Tag: noindex, nofollow`;
- the admin and its login page stay reachable, and admit **superadmins
  only** — an admin or editor with the right password is refused, and a
  session already signed in stops at its next request. The login form
  says so above itself, before anyone types: an account that works
  perfectly well is about to be turned away, and finding that out after
  submitting a password reads as a broken login rather than a closed
  site;
- superadmins browse the public site exactly as normal, so the thing
  being fixed can be looked at while it is closed.

It is superadmin-only, in the site settings dialog under **Access**, and
it asks to confirm before closing (never before reopening).

`503` is deliberate. A closed site is not a gone one: search engines
treat a `503` as temporary and keep the pages they have, for a few days
at least. A `404`, or a `200` on a "we're closed" page, is how a site
comes back from an afternoon's maintenance with its rankings gone.

### Mounting it

The setting does nothing until the host wraps its router. The lock has
to sit in front of the host's own routes — an app's API, feeds, and form
posts are not the CMS's to refuse — so mounting it is one line, and
outermost:

```go
func (a *app) routes() http.Handler {
	r := chi.NewRouter()
	// ... the app's routes, with the CMS mounted at "/" ...
	return a.logging(a.cms.Lockdown(r, "/healthz"))
}
```

Outermost, because a refused request should cost nothing underneath it —
no visitor session, no rate-limit bookkeeping, no query for a page nobody
is going to see. While the site is open the wrapper is one cached boolean
and a call through; only a closed site pays for the session lookup.

**The exempt list is the important argument.** Each entry matches the
request path exactly, or as a prefix when it ends in `/`. Two kinds of
address belong there:

- **Endpoints that are not browsers.** A container health check
  answering `503` is an orchestrator killing the site while you are
  fixing it. `/healthz` is nearly always the first entry.
- **Feeds a partner pulls on their own schedule** — a merchant catalog,
  a listings importer — where an afternoon of `503`s means listings
  dropped at the far end and days of re-import to get them back.

Everything on that list is served to the public, in full, while the rest
of the site is shut. That is the trade: name the addresses that must keep
answering, and know each one is a window into a closed site.

The admin path is never refused, exempt list or not — it is where the
lock is lifted, and locking yourself out of it would be the one mistake
this feature must not allow.

### The way back in

If the admin is unreachable for some *other* reason — a lost superadmin
password, a broken login — the lock can be forced from the environment:

```
CMS_SITE_LOCKED=false
```

`Config.LockOverride` behind it, and it wins over the stored setting in
both directions: `false` reopens a locked site, `true` brings one up
closed. It does not change what is stored, so removing the variable
returns the site to whatever the admin last saved. The stored value can
also be flipped directly — it is one row:

```sql
UPDATE cms_settings SET value = '' WHERE key = 'site_locked';
```

A locked site stamps a red **Site closed** badge under the admin's brand
on every page, for the same reason development mode stamps its own: the
failure this invites is nobody remembering the switch is on.

### The page visitors see

By default the CMS serves a small built-in notice that depends on none of
the site's templates, stylesheets, or media — because any of those may be
exactly what is broken. To brand it, set `Config.LockedHandler` to a
handler of your own; the status and headers are already set when it is
called, so write the body and leave the status alone.
