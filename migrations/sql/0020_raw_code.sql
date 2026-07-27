-- Per-page and site-wide CSS/JS became raw head/body markup: a field may
-- carry its own <style>, <link>, or <script> tags, so the separate
-- external-URL lists are gone.
ALTER TABLE cms_pages DROP COLUMN css_links;
ALTER TABLE cms_pages DROP COLUMN js_links;
DELETE FROM cms_settings WHERE key IN ('site_css_links', 'site_js_links');
