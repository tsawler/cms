CREATE TABLE cms_media_folders (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    -- Folder names are free text an editor types, and unlike slugs and email
    -- addresses nothing normalizes their case before the unique index sees
    -- them. MySQL's default collation is case-insensitive, so "Photos" and
    -- "photos" would collide here but not on Postgres. utf8mb4_bin restores
    -- the Postgres behaviour and is spelled the same on MySQL and MariaDB
    -- (utf8mb4_0900_as_cs would be MySQL-only).
    name       VARCHAR(191) COLLATE utf8mb4_bin NOT NULL UNIQUE,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

-- Folders are metadata only: bucket object keys never change, so moving a
-- file between folders is a plain UPDATE.
ALTER TABLE cms_media ADD COLUMN folder_id BIGINT;
ALTER TABLE cms_media ADD CONSTRAINT cms_media_folder_fk
    FOREIGN KEY (folder_id) REFERENCES cms_media_folders (id) ON DELETE SET NULL;

CREATE INDEX cms_media_folder_idx ON cms_media (folder_id);
