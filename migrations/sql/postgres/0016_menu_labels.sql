-- Per-locale menu labels. The existing label column remains the default
-- locale's text; labels holds overrides keyed by locale, e.g.
-- {"fr": "À propos"}. Riding on the row (rather than a child table) lets
-- ReplaceMenu's wipe-and-reinsert carry every locale through an edit made
-- in any one of them.
ALTER TABLE cms_menu_items ADD COLUMN labels JSONB NOT NULL DEFAULT '{}';
