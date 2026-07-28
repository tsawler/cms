CREATE TABLE cms_media (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    s3_key      VARCHAR(255) NOT NULL UNIQUE, -- key prefix; objects live at <prefix>/original.<ext> etc.
    filename    TEXT NOT NULL,
    mime        TEXT NOT NULL,
    ext         TEXT NOT NULL,
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    size        BIGINT NOT NULL DEFAULT 0,
    uploaded_by BIGINT,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    FOREIGN KEY (uploaded_by) REFERENCES cms_users (id) ON DELETE SET NULL
);

CREATE TABLE cms_media_meta (
    media_id BIGINT NOT NULL,
    locale   VARCHAR(16) NOT NULL,
    alt_text TEXT NOT NULL DEFAULT (''),
    PRIMARY KEY (media_id, locale),
    FOREIGN KEY (media_id) REFERENCES cms_media (id) ON DELETE CASCADE
);

-- Blocks gain an image kind: the block's content is the image URL.
ALTER TABLE cms_blocks DROP CONSTRAINT cms_blocks_kind_check;
ALTER TABLE cms_blocks ADD CONSTRAINT cms_blocks_kind_check
    CHECK (kind IN ('text', 'html', 'image'));
