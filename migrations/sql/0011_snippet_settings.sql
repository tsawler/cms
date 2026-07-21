-- Admin-created snippets can be section presets: a non-null settings
-- object holds the section settings (bg, width, height, valign) the
-- editor applies when the snippet starts a new section. NULL means an
-- ordinary inline block, matching the "non-nil Settings = preset"
-- convention config snippets use.
ALTER TABLE cms_snippets ADD COLUMN settings JSONB;
