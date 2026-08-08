-- MySQL/MariaDB port of 0029: optional two-factor login (TOTP). See the
-- Postgres file for what the columns mean; per this directory's
-- conventions the TEXT column becomes a VARCHAR (a 160-bit base32 secret
-- is 32 characters).
ALTER TABLE cms_users ADD COLUMN totp_secret VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE cms_users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
