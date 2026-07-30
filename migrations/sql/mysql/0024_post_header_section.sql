-- A post's header stops being a field and becomes a section. See the
-- Postgres file of the same name for why.
--
-- The extra work here is the foreign key: InnoDB refuses to drop a column a
-- constraint still references, and 0023 added that constraint without naming
-- it, so the generated name has to be looked up before it can be dropped.
-- A NULL lookup means the key is already gone, and runs a harmless SELECT
-- rather than a CONCAT of NULL that PREPARE would reject.
SET @fk := (
    SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'cms_posts'
      AND COLUMN_NAME = 'header_media_id'
      AND REFERENCED_TABLE_NAME IS NOT NULL
    LIMIT 1);
SET @sql := IF(@fk IS NULL, 'SELECT 1',
    CONCAT('ALTER TABLE cms_posts DROP FOREIGN KEY ', @fk));
PREPARE cms_drop_header_fk FROM @sql;
EXECUTE cms_drop_header_fk;
DEALLOCATE PREPARE cms_drop_header_fk;

ALTER TABLE cms_posts
    DROP COLUMN header_media_id,
    DROP COLUMN header_url;
