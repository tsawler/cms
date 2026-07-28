-- Media gains a video kind: MP4/WebM stored as uploaded (no transcoding),
-- with an optional client-captured poster frame as the web/thumb variants.
ALTER TABLE cms_media DROP CONSTRAINT cms_media_kind_check;
ALTER TABLE cms_media ADD CONSTRAINT cms_media_kind_check
    CHECK (kind IN ('image', 'file', 'video'));
