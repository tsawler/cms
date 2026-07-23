-- Folders are scoped to one media kind: a folder created on the images
-- tab (or from the image picker) exists only among images, and likewise
-- for documents and videos. Existing folders take the kind of whatever
-- they mostly hold; empty ones default to images.
ALTER TABLE cms_media_folders ADD COLUMN kind TEXT NOT NULL DEFAULT 'image';

UPDATE cms_media_folders f SET kind = (
    SELECT m.kind FROM cms_media m WHERE m.folder_id = f.id
    GROUP BY m.kind ORDER BY count(*) DESC, m.kind LIMIT 1)
WHERE EXISTS (SELECT 1 FROM cms_media m WHERE m.folder_id = f.id);

-- Names now need to be unique only within a kind.
ALTER TABLE cms_media_folders DROP CONSTRAINT cms_media_folders_name_key;
ALTER TABLE cms_media_folders ADD CONSTRAINT cms_media_folders_kind_name_key UNIQUE (kind, name);
