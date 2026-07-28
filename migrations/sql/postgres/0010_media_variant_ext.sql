-- Image variants (web, thumb) are encoded as WebP for new uploads; the
-- extension of each record's variant objects is now stored rather than
-- derived from the original's mime type. Existing rows keep the legacy
-- derivation (.png for PNG originals, .jpg otherwise) so their bucket
-- objects stay addressable.
ALTER TABLE cms_media ADD COLUMN variant_ext TEXT NOT NULL DEFAULT '';

UPDATE cms_media
SET variant_ext = CASE WHEN mime = 'image/png' THEN '.png' ELSE '.jpg' END
WHERE kind = 'image';
