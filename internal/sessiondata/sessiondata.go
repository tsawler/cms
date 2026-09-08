// Package sessiondata holds the session keys and the small pieces of
// session plumbing shared across packages: the admin area and the public
// handler (which needs to recognize logged-in editors for in-place
// editing), and the two session stores.
package sessiondata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/alexedwards/scs/v2"
)

// storeWriteTimeout bounds one session write. A write is a single
// statement against one row, so anything approaching this is a store in
// trouble; the point is that it ends rather than that it fits.
const storeWriteTimeout = 5 * time.Second

// WriteContext is the context a session store should write under, given
// the one the request arrived with.
//
// Reads take the request's context unchanged: a visitor who has gone away
// does not need their session looked up, and letting those queries pile
// up behind a slow store is exactly what a cancellable read prevents.
//
// Writes are different, and this is the whole reason the helper exists. A
// session write happens because something has already been decided — a
// login granted, a session destroyed on logout, a token renewed against
// fixation — and a client that hangs up in the moment between the
// decision and the write should not undo it. A logout that did not delete
// the session because the browser went away leaves a session that still
// works, which is the wrong way for that to fail.
//
// So the cancellation is dropped and a deadline put in its place: the
// write survives the disconnect, and still cannot run forever. The caller
// must call the returned cancel.
func WriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), storeWriteTimeout)
}

const (
	KeyUserID = "cmsUserID"
	KeyCSRF   = "cmsCSRFToken"
	KeyFlash  = "cmsFlash"
)

// EnsureCSRF returns the session's CSRF token, generating and storing one
// if the session doesn't have one yet.
func EnsureCSRF(ctx context.Context, sessions *scs.SessionManager) (string, error) {
	token := sessions.GetString(ctx, KeyCSRF)
	if token != "" {
		return token, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token = hex.EncodeToString(buf)
	sessions.Put(ctx, KeyCSRF, token)
	return token, nil
}
