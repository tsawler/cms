package auth

// Password reset tokens: minted when somebody asks for a reset link,
// consumed when the link is used. The store never holds a usable token —
// only its SHA-256 — so the email is the token's single copy, and this
// table leaks nothing a browser could replay.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// ResetTTL is how long a reset link works. Long enough to walk to another
// device and open an inbox; short enough that a link forwarded or left in
// an abandoned mailbox goes stale the same hour it arrived.
const ResetTTL = time.Hour

// ErrResetInvalid is returned for a token that is unknown, expired, or
// already used. Deliberately one error for all three: distinguishing them
// would tell a guesser which failures were near-misses.
var ErrResetInvalid = errors.New("auth: reset token invalid or expired")

// MintReset creates a reset token for the user and returns the one usable
// copy of it. Any previous token the user held is revoked — asking twice
// leaves one working link, the newest — and expired rows are swept while
// we are here, so the table cannot accumulate.
func (s *Store) MintReset(ctx context.Context, userID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now().UTC()
	if _, err := s.db.Exec(ctx, `
		DELETE FROM cms_password_resets WHERE user_id = $1 OR expires_at < $2`,
		userID, now); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO cms_password_resets (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`,
		hashResetToken(token), userID, now.Add(ResetTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// ResetUser returns the user a live token belongs to, without spending
// it. This is the GET half of the flow — showing the new-password form —
// which must not consume anything, because rendering a form is not using
// it: the token has to survive until the form actually comes back.
func (s *Store) ResetUser(ctx context.Context, token string) (*User, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+prefixedUserColumns+`
		FROM cms_password_resets r
		JOIN cms_users u ON u.id = r.user_id
		WHERE r.token_hash = $1 AND r.expires_at > $2`,
		hashResetToken(token), time.Now().UTC())
	u, err := scanUser(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrResetInvalid
	}
	return u, err
}

// ConsumeReset spends a live token: the row is deleted and the user it
// belonged to is returned, exactly once. A second call with the same
// token — a replayed link, a double submit — finds no row and gets
// ErrResetInvalid, which is the property that makes these single-use.
//
// The delete is the claim. DELETE ... RETURNING would be the elegant
// spelling, but MySQL has no RETURNING; deleting by hash first and only
// then looking the user up would tell a racing duplicate that it lost,
// though not who won — and losing is the correct outcome for it.
func (s *Store) ConsumeReset(ctx context.Context, token string) (*User, error) {
	u, err := s.ResetUser(ctx, token)
	if err != nil {
		return nil, err
	}
	tag, err := s.db.Exec(ctx,
		`DELETE FROM cms_password_resets WHERE token_hash = $1`, hashResetToken(token))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// Somebody else's delete landed between our read and ours: the
		// token was spent, just not by us.
		return nil, ErrResetInvalid
	}
	return u, nil
}

// prefixedUserColumns is userColumns qualified for the join above.
const prefixedUserColumns = "u.id, u.email, u.name, u.password_hash, u.role, u.active, u.totp_secret, u.totp_last_step, u.created_at, u.updated_at"

// hashResetToken is the stored form of a token. Plain SHA-256 rather than
// argon2: these are 256-bit random strings, not passwords, so there is
// nothing for a work factor to protect against guessing.
func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
