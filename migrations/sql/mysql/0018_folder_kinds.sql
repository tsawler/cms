-- Folders are scoped to one media kind: a folder created on the images
-- tab (or from the image picker) exists only among images, and likewise
-- for documents and videos. Existing folders take the kind of whatever
-- they mostly hold; empty ones default to images.
ALTER TABLE cms_media_folders ADD COLUMN kind VARCHAR(32) NOT NULL DEFAULT 'image';

-- The subquery reads cms_media while cms_media_folders is being updated, so
-- it does not hit MySQL's "can't specify target table for update" rule.
UPDATE cms_media_folders f SET kind = (
    SELECT m.kind FROM cms_media m WHERE m.folder_id = f.id
    GROUP BY m.kind ORDER BY count(*) DESC, m.kind LIMIT 1)
WHERE EXISTS (SELECT 1 FROM cms_media m WHERE m.folder_id = f.id);

-- Names now need to be unique only within a kind. The UNIQUE in 0005 became
-- an index named after its column, which is what DROP INDEX takes here;
-- Postgres drops the equivalent named constraint instead.
ALTER TABLE cms_media_folders DROP INDEX name;
ALTER TABLE cms_media_folders ADD CONSTRAINT cms_media_folders_kind_name_key UNIQUE (kind, name);
