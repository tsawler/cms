-- Point a post's images at the media library instead of freezing one
-- rendition's URL into the row.
--
-- thumbnail_url and header_url stored whichever URL the picker happened to
-- offer — the full-width "web" rendition — so a listing card of twelve
-- posts downloaded twelve full-width images and scaled them down in the
-- browser. With the media id the renderer chooses the rendition at request
-- time: the card size for a listing thumbnail, the web size for a header,
-- and a srcset of the rest. Adding a rung to the ladder later then reflows
-- every existing post, instead of only the ones saved afterwards.
--
-- The URL columns stay, and stay authoritative when no id is set: they hold
-- images that are not in the library at all — an absolute URL to somewhere
-- else, or a path the host site serves itself.
--
-- ON DELETE SET NULL also fixes a quieter bug: deleting a library image
-- used to leave posts pointing at a dead URL, and the admin could only warn
-- about it. Now the reference clears itself and the post simply shows no
-- image.
ALTER TABLE cms_posts
    ADD COLUMN thumbnail_media_id BIGINT REFERENCES cms_media (id) ON DELETE SET NULL,
    ADD COLUMN header_media_id    BIGINT REFERENCES cms_media (id) ON DELETE SET NULL;

-- Postgres does not index a foreign key for you, and both columns are
-- scanned whenever a media item is deleted.
CREATE INDEX cms_posts_thumbnail_media_idx ON cms_posts (thumbnail_media_id);
CREATE INDEX cms_posts_header_media_idx ON cms_posts (header_media_id);

-- Adopt the URLs already stored. Every object of a library image lives
-- under its item id, so a stored URL that names one — "/cms/media/<item
-- id>/web.webp" — identifies the row it came from. Item ids are 24 random
-- hex characters, so a match is the image and not a coincidence.
--
-- A URL matching nothing keeps its column and goes on being served as it is:
-- that is the external-image case, which is exactly what the columns now
-- mean. A URL that does match is cleared, so each image has one source of
-- truth.
UPDATE cms_posts p
SET thumbnail_media_id = (
        SELECT m.id FROM cms_media m
        WHERE m.kind = 'image' AND p.thumbnail_url LIKE '%/' || m.store_key || '/%'
        LIMIT 1)
WHERE p.thumbnail_url <> '';

UPDATE cms_posts p
SET header_media_id = (
        SELECT m.id FROM cms_media m
        WHERE m.kind = 'image' AND p.header_url LIKE '%/' || m.store_key || '/%'
        LIMIT 1)
WHERE p.header_url <> '';

UPDATE cms_posts SET thumbnail_url = '' WHERE thumbnail_media_id IS NOT NULL;
UPDATE cms_posts SET header_url = '' WHERE header_media_id IS NOT NULL;
