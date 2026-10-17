-- Widen the columns that hold content from TEXT to LONGTEXT.
--
-- MySQL's TEXT stops at 64 KiB. Postgres's has no practical limit, and the
-- admin accepts far more than 64 KiB in one save (a region body may be
-- 5 MiB), so a long sections region, a large site stylesheet or a big
-- compiled Tailwind build saved on Postgres and failed on MySQL — with
-- "Data too long" under strict mode, and by silent truncation without it.
-- LONGTEXT is what cms_page_versions.payload and cms_search_docs.body have
-- used from the start; in InnoDB it costs nothing over TEXT for short
-- values, which are stored the same way.
--
-- MODIFY restates each column whole. None of these carries a column-level
-- collation, so restating the type and default loses nothing.
--
-- On a large site this rewrites cms_blocks, which takes a while and, like
-- every MySQL DDL statement, commits on its own.

ALTER TABLE cms_blocks MODIFY content LONGTEXT NOT NULL DEFAULT ('');

ALTER TABLE cms_pages
    MODIFY head_css LONGTEXT NOT NULL DEFAULT (''),
    MODIFY body_js  LONGTEXT NOT NULL DEFAULT ('');

ALTER TABLE cms_page_drafts
    MODIFY head_css LONGTEXT NOT NULL DEFAULT (''),
    MODIFY body_js  LONGTEXT NOT NULL DEFAULT ('');

ALTER TABLE cms_settings MODIFY value LONGTEXT NOT NULL;

ALTER TABLE cms_snippets MODIFY html LONGTEXT NOT NULL DEFAULT ('');

ALTER TABLE cms_code_snippets MODIFY html LONGTEXT NOT NULL DEFAULT ('');

ALTER TABLE cms_content_css MODIFY css LONGTEXT NOT NULL;
