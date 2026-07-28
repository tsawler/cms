-- Narrow the keyed and enumerated columns from TEXT to VARCHAR(n), matching
-- the lengths the MySQL schema has used since 0001.
--
-- MySQL forced the change there: InnoDB cannot index a TEXT column without a
-- prefix length. Applying it here too keeps the two engines behaving
-- identically at the boundaries — without it a 300-character slug would save
-- on Postgres and fail on MySQL, which is exactly the class of difference
-- the conformance suite exists to rule out.
--
-- On an existing install this rewrites the affected tables and will FAIL
-- LOUDLY if any stored value is longer than its new limit. That is
-- deliberate: silently truncating a slug would change page URLs. The lengths
-- below are far above anything the admin UI can produce, so a failure means
-- data written by something else and wants a look before it is forced
-- through.

ALTER TABLE cms_users
    ALTER COLUMN email TYPE VARCHAR(255),
    ALTER COLUMN role TYPE VARCHAR(32);

ALTER TABLE cms_sessions
    ALTER COLUMN token TYPE VARCHAR(128);

ALTER TABLE cms_pages
    ALTER COLUMN slug TYPE VARCHAR(255),
    ALTER COLUMN status TYPE VARCHAR(32),
    ALTER COLUMN visibility TYPE VARCHAR(32);

ALTER TABLE cms_page_meta
    ALTER COLUMN locale TYPE VARCHAR(16),
    ALTER COLUMN status TYPE VARCHAR(32);

ALTER TABLE cms_blocks
    ALTER COLUMN region TYPE VARCHAR(64),
    ALTER COLUMN locale TYPE VARCHAR(16),
    ALTER COLUMN status TYPE VARCHAR(32),
    ALTER COLUMN kind TYPE VARCHAR(32),
    ALTER COLUMN snippet_key TYPE VARCHAR(191);

ALTER TABLE cms_media
    ALTER COLUMN s3_key TYPE VARCHAR(255),
    ALTER COLUMN kind TYPE VARCHAR(32),
    ALTER COLUMN variant_ext TYPE VARCHAR(16);

ALTER TABLE cms_media_meta
    ALTER COLUMN locale TYPE VARCHAR(16);

ALTER TABLE cms_media_folders
    ALTER COLUMN name TYPE VARCHAR(191),
    ALTER COLUMN kind TYPE VARCHAR(32);

ALTER TABLE cms_snippets
    ALTER COLUMN name TYPE VARCHAR(191);

ALTER TABLE cms_menu_items
    ALTER COLUMN menu TYPE VARCHAR(64);

ALTER TABLE cms_posts
    ALTER COLUMN feed TYPE VARCHAR(32);

ALTER TABLE cms_settings
    ALTER COLUMN key TYPE VARCHAR(191);
