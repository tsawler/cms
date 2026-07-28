ALTER TABLE cms_media ADD COLUMN kind TEXT NOT NULL DEFAULT 'image'
    CHECK (kind IN ('image', 'file'));
