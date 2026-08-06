-- Password reset tokens, for the login page's "forgot password" flow.
--
-- The primary key is a SHA-256 of the token, never the token itself: the
-- only copy of the real token is in the email, so a read of this table
-- (a leaked backup, a curious query) yields nothing a browser can use.
--
-- Rows are single-use and short-lived. Consuming a token deletes its
-- row, minting a new one deletes the user's previous rows, and expired
-- rows are swept opportunistically on each mint — so the table holds at
-- most one live row per user who currently has a reset email in flight.
CREATE TABLE cms_password_resets (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES cms_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX cms_password_resets_user_idx ON cms_password_resets (user_id);
