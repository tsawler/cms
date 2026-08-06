-- MySQL/MariaDB port of 0028: password reset tokens. See the Postgres
-- file for what the table is; per this directory's conventions the keyed
-- TEXT column becomes a VARCHAR (a SHA-256 hex digest is 64 characters)
-- and the timestamps become DATETIME(6) holding UTC.
CREATE TABLE cms_password_resets (
    token_hash VARCHAR(64) PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (user_id) REFERENCES cms_users(id) ON DELETE CASCADE
);

CREATE INDEX cms_password_resets_user_idx ON cms_password_resets (user_id);
