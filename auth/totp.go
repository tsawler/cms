package auth

// Time-based one-time passwords (RFC 6238), for the admin's optional
// two-factor login. Codes are the standard authenticator-app shape — six
// digits, SHA-1, a 30-second period — because that is what every app
// generates; anything fancier would just fail to enroll.
//
// The store half lives here too: a user's secret, and the "last used
// step" that makes each code single-use. Accepting a valid code twice
// would turn a shoulder-surfed or phished code into a working credential
// for its whole 90-second window.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// totpPeriod is the code lifetime every authenticator app assumes.
const totpPeriod = 30 * time.Second

// totpEncoding is unpadded base32, the alphabet authenticator apps take
// for manually-entered keys ("=" padding confuses several of them).
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh base32 secret for enrolling an
// authenticator app: 160 bits, RFC 4226's recommended key size.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generating totp secret: %w", err)
	}
	return totpEncoding.EncodeToString(raw), nil
}

// TOTPCode returns the six-digit code for the secret at time t — what an
// authenticator app holding the same secret shows at that moment.
func TOTPCode(secret string, t time.Time) (string, error) {
	return hotp(secret, t.Unix()/int64(totpPeriod/time.Second))
}

// VerifyTOTP reports whether code matches the secret at time t, accepting
// one step of clock skew either side. On success it returns the step the
// code matched, which callers must claim (Store.ConsumeTOTPStep) so the
// same code cannot be accepted twice.
func VerifyTOTP(secret, code string, t time.Time) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	now := t.Unix() / int64(totpPeriod/time.Second)
	for _, delta := range []int64{0, -1, 1} {
		s := now + delta
		if s < 0 {
			continue
		}
		want, err := hotp(secret, s)
		if err != nil {
			return 0, false
		}
		// Constant time, same as passwords: a byte-by-byte compare would
		// let response timing leak how much of a guess was right.
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// hotp is RFC 4226's dynamic truncation over HMAC-SHA1, producing the
// six-digit code for one counter value.
func hotp(secret string, counter int64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("auth: malformed totp secret: %w", err)
	}
	mac := hmac.New(sha1.New, key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// TOTPProvisioningURI builds the otpauth:// URL an authenticator app
// enrolls from (usually via a QR code): issuer and account label the
// entry in the app, secret is the shared key.
func TOTPProvisioningURI(issuer, account, secret string) string {
	v := url.Values{
		"secret":    {secret},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {"30"},
	}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// TwoFactorEnabled reports whether the user has finished enrolling an
// authenticator app. Enrollment is only saved once a live code has
// confirmed it, so a non-empty secret is the whole answer.
func (u *User) TwoFactorEnabled() bool { return u.TOTPSecret != "" }

// EnableTOTP stores a confirmed secret, turning two-factor on for the
// user. confirmedStep is the step of the code that proved the enrollment;
// recording it spends that code, so it cannot be replayed at the next
// login.
func (s *Store) EnableTOTP(ctx context.Context, id int64, secret string, confirmedStep int64) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE cms_users
		SET totp_secret = $1, totp_last_step = $2, updated_at = now()
		WHERE id = $3`,
		secret, confirmedStep, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DisableTOTP turns two-factor off for the user — their own choice on the
// settings page, or an admin rescuing somebody who lost their phone.
func (s *Store) DisableTOTP(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE cms_users
		SET totp_secret = '', totp_last_step = 0, updated_at = now()
		WHERE id = $1`,
		id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ConsumeTOTPStep claims the step a verified code matched, exactly once.
// It reports false when the step was already claimed — a replayed code —
// which callers must treat as a failed login. The guard is the WHERE
// clause, so two racing submissions of one code resolve in the database:
// one wins, the other reads zero rows.
func (s *Store) ConsumeTOTPStep(ctx context.Context, id int64, step int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE cms_users
		SET totp_last_step = $1, updated_at = now()
		WHERE id = $2 AND totp_last_step < $1`,
		step, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
