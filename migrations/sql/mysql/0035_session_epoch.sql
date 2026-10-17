-- session_epoch counts changes to how an account signs in: a new password
-- (changed, reset, or set by an admin) and two-factor switched on or off.
-- A session records the epoch it was granted under, and is honoured only
-- while the two still match, so a credential change ends every other
-- session the account has — including the stolen one that prompted it.
-- Sessions from before this column carry no epoch, which reads as 0, the
-- default here: they keep working until the account's first change.
ALTER TABLE cms_users ADD COLUMN session_epoch BIGINT NOT NULL DEFAULT 0;
