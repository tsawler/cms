// Package sessiondata holds the session keys and helpers shared by the
// admin area and the public handler (which needs to recognize logged-in
// editors for in-place editing).
package sessiondata

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/alexedwards/scs/v2"
)

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
