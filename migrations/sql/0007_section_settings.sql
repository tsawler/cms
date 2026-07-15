-- Section blocks carry curated presentation settings (background, width)
-- chosen by editors; other block kinds leave this empty.
ALTER TABLE cms_blocks ADD COLUMN settings JSONB NOT NULL DEFAULT '{}';
