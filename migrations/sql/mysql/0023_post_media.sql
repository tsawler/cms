-- Point a post's images at the media library instead of freezing one
-- rendition's URL into the row. See the Postgres file of the same name for
-- why; the differences here are dialect only — CONCAT for string joining,
-- and no explicit indexes, since InnoDB creates one for every foreign key.
ALTER TABLE cms_posts
    ADD COLUMN thumbnail_media_id BIGINT,
    ADD COLUMN header_media_id    BIGINT,
    ADD FOREIGN KEY (thumbnail_media_id) REFERENCES cms_media (id) ON DELETE SET NULL,
    ADD FOREIGN KEY (header_media_id) REFERENCES cms_media (id) ON DELETE SET NULL;

UPDATE cms_posts p
SET thumbnail_media_id = (
        SELECT m.id FROM cms_media m
        WHERE m.kind = 'image' AND p.thumbnail_url LIKE CONCAT('%/', m.store_key, '/%')
        LIMIT 1)
WHERE p.thumbnail_url <> '';

UPDATE cms_posts p
SET header_media_id = (
        SELECT m.id FROM cms_media m
        WHERE m.kind = 'image' AND p.header_url LIKE CONCAT('%/', m.store_key, '/%')
        LIMIT 1)
WHERE p.header_url <> '';

UPDATE cms_posts SET thumbnail_url = '' WHERE thumbnail_media_id IS NOT NULL;
UPDATE cms_posts SET header_url = '' WHERE header_media_id IS NOT NULL;
