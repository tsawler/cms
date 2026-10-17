-- Match page slugs byte for byte, as Postgres does.
--
-- Under MySQL's default collation a lookup ignores case and accents, so
-- /About and /ABOUT served the page stored at "about" on MySQL while
-- Postgres answered 404: duplicate URLs for search engines, and one more
-- way the engines disagreed. Slugs are always stored lowercase (see
-- content.NormalizeSlug), so the unique index loses nothing by becoming
-- exact. utf8mb4_bin is what 0005 already gives media folder names.
ALTER TABLE cms_pages MODIFY slug VARCHAR(255) COLLATE utf8mb4_bin NOT NULL;
