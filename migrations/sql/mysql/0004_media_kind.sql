ALTER TABLE cms_media ADD COLUMN kind VARCHAR(32) NOT NULL DEFAULT 'image';
ALTER TABLE cms_media ADD CONSTRAINT cms_media_kind_check
    CHECK (kind IN ('image', 'file'));
