-- Site-wide settings behind the in-place editor's "Site settings"
-- dialog (menu alignment, site name, logo today). A key/value shape so
-- future settings need no schema change.
--
-- `key` is a reserved word in MySQL and must be quoted wherever it appears.
CREATE TABLE cms_settings (
    `key`   VARCHAR(191) PRIMARY KEY,
    value TEXT NOT NULL
);
