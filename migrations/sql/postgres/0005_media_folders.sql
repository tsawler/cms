CREATE TABLE cms_media_folders (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Folders are metadata only: bucket object keys never change, so moving a
-- file between folders is a plain UPDATE.
ALTER TABLE cms_media ADD COLUMN folder_id BIGINT
    REFERENCES cms_media_folders (id) ON DELETE SET NULL;

CREATE INDEX cms_media_folder_idx ON cms_media (folder_id);
