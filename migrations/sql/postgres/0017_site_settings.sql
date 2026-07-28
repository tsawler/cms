-- Site-wide settings behind the in-place editor's "Site settings"
-- dialog (menu alignment, site name, logo today). A key/value shape so
-- future settings need no schema change.
CREATE TABLE cms_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
