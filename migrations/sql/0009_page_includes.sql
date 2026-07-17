-- Per-page external resource URLs (newline-separated). Rendered by
-- cmsHead as <link rel="stylesheet"> tags (before the inline CSS, so page
-- CSS can override the library) and by cmsScripts as <script src> tags
-- (before the inline JS, so page JS can use the library).
ALTER TABLE cms_pages ADD COLUMN css_links TEXT NOT NULL DEFAULT '';
ALTER TABLE cms_pages ADD COLUMN js_links TEXT NOT NULL DEFAULT '';
