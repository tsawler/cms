ALTER TABLE cms_pages ADD COLUMN visibility VARCHAR(32) NOT NULL DEFAULT 'public';
ALTER TABLE cms_pages ADD CONSTRAINT cms_pages_visibility_check
    CHECK (visibility IN ('public', 'private'));
