CREATE TABLE cms_media (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    s3_key      TEXT NOT NULL UNIQUE, -- key prefix; objects live at <prefix>/original.<ext> etc.
    filename    TEXT NOT NULL,
    mime        TEXT NOT NULL,
    ext         TEXT NOT NULL,
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    size        BIGINT NOT NULL DEFAULT 0,
    uploaded_by BIGINT REFERENCES cms_users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cms_media_meta (
    media_id BIGINT NOT NULL REFERENCES cms_media (id) ON DELETE CASCADE,
    locale   TEXT NOT NULL,
    alt_text TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (media_id, locale)
);

-- Blocks gain an image kind: the block's content is the image URL.
ALTER TABLE cms_blocks DROP CONSTRAINT cms_blocks_kind_check;
ALTER TABLE cms_blocks ADD CONSTRAINT cms_blocks_kind_check
    CHECK (kind IN ('text', 'html', 'image'));
