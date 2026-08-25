package cms

// Lockdown: the switch that closes the whole site to everyone but
// superadmins.
//
// The setting behind it is content.SiteSettings.Locked, thrown from the
// editor's Site settings dialog by a superadmin (or forced from the
// environment with CMS_SITE_LOCKED). Enforcing it takes two doors, and
// this file is the outer one:
//
//   - Here, in front of the host's whole router: every public address
//     answers 503 unless the request carries a superadmin's session.
//
//   - In the admin package, at login and on every signed-in request: an
//     admin or editor is refused the panel too, so a locked site does not
//     merely hide its pages from the people who edit them.
//
// The admin path itself is never refused here. It has to stay reachable
// or a superadmin could not sign in to lift the lock — which is the
// difference between this and stopping the process, and the whole reason
// the switch exists.

import (
	"context"
	"net/http"
	"strings"

	"github.com/tsawler/cms/internal/sessiondata"
)

// lockedRetryAfter is what a refused response tells a crawler to wait
// before trying again. An hour: long enough that a 503 does not read as
// a flap, short enough that a site opened again is re-crawled the same
// day. Google treats a 503 shorter than a few days as temporary and
// keeps the pages it has, which is the entire point of answering 503
// rather than 404 here.
const lockedRetryAfter = "3600"

// Lockdown wraps the host's router with the site lock: while the site is
// locked, every request that is not exempt, not under AdminPath, and not
// carrying a superadmin's session is refused with 503.
//
// Mount it outermost, in front of the host's own middleware:
//
//	return a.logging(cms.Lockdown(router, "/healthz"))
//
// Outermost, because a refused request should cost nothing downstream —
// no visitor session, no rate-limit bookkeeping, no database write for a
// page nobody is going to see.
//
// While the site is open the wrapper is a single cached boolean read and
// a call to next; it adds no query and touches no session. Only a locked
// site pays for the session lookup, and only on the requests it is about
// to refuse or let a superadmin through.
//
// # What stays reachable
//
// AdminPath and everything under it, always: the login page, the admin,
// and the admin's own assets. The admin refuses non-superadmins itself.
//
// Each exempt entry is matched against the request path exactly, or as a
// prefix when it ends in "/". Two kinds of address belong there and
// almost nothing else does:
//
//   - The endpoints something other than a browser depends on — a
//     container's health check, a payment provider's webhook. A health
//     check answering 503 is an orchestrator killing the site while a
//     superadmin is trying to fix it.
//
//   - Feeds a partner pulls on their own schedule, where being down for
//     an afternoon means listings dropped at the far end and days of
//     re-import to get them back.
//
// An exempt address is served to the public in full while the site is
// closed. That is the trade: name the ones that must keep answering, and
// know that each is a window into a site everyone else is told is shut.
func (c *CMS) Lockdown(next http.Handler, exempt ...string) http.Handler {
	adminPath := c.cfg.AdminPath // normalized by New: leading slash, no trailing slash
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.SiteLocked(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if path == adminPath || strings.HasPrefix(path, adminPath+"/") {
			next.ServeHTTP(w, r)
			return
		}
		for _, e := range exempt {
			if path == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(path, e)) {
				next.ServeHTTP(w, r)
				return
			}
		}
		// Everyone else has to prove they are a superadmin, which means
		// reading the session this request carries — if it carries one.
		ctx, ok := c.superadminContext(r)
		if !ok {
			c.serveLocked(w, r)
			return
		}
		// The loaded session rides along, so the handler underneath —
		// the CMS's own page handler, in the usual mounting — finds it
		// already in the context instead of fetching it a second time.
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// superadminContext loads the CMS session for a request and reports
// whether it belongs to an active superadmin, returning the context the
// session was loaded into so the rest of the chain can reuse it.
//
// It deliberately does not use the session middleware: this is a read,
// and running LoadAndSave here would commit a session and set a cookie
// on responses that are about to be refused — putting a Set-Cookie on a
// 503 served to a crawler, and a write on the database of a site that is
// closed precisely because something is wrong with it.
//
// A masquerading superadmin reads as whoever they are pretending to be,
// so a superadmin working as an editor is refused like that editor. That
// is what masquerade means, and the way out is to end it — the exit
// button is in the admin, which is never locked.
func (c *CMS) superadminContext(r *http.Request) (context.Context, bool) {
	if c.sessions == nil || c.users == nil {
		return nil, false
	}
	token := ""
	if ck, err := r.Cookie(c.sessions.Cookie.Name); err == nil {
		token = ck.Value
	}
	ctx, err := c.sessions.Load(r.Context(), token)
	if err != nil {
		c.cfg.Logger.Error("cms: reading the session for the site lock", "err", err)
		return nil, false
	}
	id := c.sessions.GetInt64(ctx, sessiondata.KeyUserID)
	if id == 0 {
		return nil, false
	}
	u, err := c.users.GetByID(ctx, id)
	if err != nil || !u.Active || !u.Role.IsSuperadmin() {
		return nil, false
	}
	return ctx, true
}

// serveLocked answers a request the lock refused.
//
// 503 with a Retry-After, never 200 and never 404: the site is not gone
// and it is not empty, it is shut for the moment, and that is a distinct
// thing to say to a crawler that has years of this site's URLs. The
// noindex rides along for the crawler that ignores the status, and
// no-store keeps a proxy from serving the closed notice after the site
// opens again.
func (c *CMS) serveLocked(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", lockedRetryAfter)
	w.Header().Set("X-Robots-Tag", robotsDirective)
	w.Header().Set("Cache-Control", "no-store")
	if c.cfg.LockedHandler != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		c.cfg.LockedHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(lockedPage)
}

// lockedPage is the built-in notice, kept deliberately plain: it is
// served by a site whose own templates, stylesheet, and media may be
// exactly what is broken, so it depends on none of them. A host that
// wants its own branding sets Config.LockedHandler.
//
// It says nothing about why, and offers no link to the admin. A closed
// site should not advertise where its door is.
var lockedPage = []byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Temporarily unavailable</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32rem; margin: 20vh auto; padding: 0 1.5rem; color: #1f2937; text-align: center;">
<h1 style="font-size: 1.5rem; font-weight: 600;">We&rsquo;ll be back shortly</h1>
<p style="color: #6b7280;">The site is closed for maintenance. Please try again later.</p>
</body></html>
`)
