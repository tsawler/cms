-- Optional two-factor login (TOTP authenticator apps).
--
-- totp_secret is the base32 key an authenticator app was enrolled with,
-- '' while two-factor is off. It is only written once a live code has
-- confirmed the enrollment, so a non-empty secret means "on" — there is
-- no separate enabled flag to fall out of sync with it.
--
-- totp_last_step is the RFC 6238 time step of the last code accepted for
-- this user. A login only succeeds by moving it forward, which is what
-- makes each code single-use (see auth.Store.ConsumeTOTPStep).
ALTER TABLE cms_users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE cms_users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
