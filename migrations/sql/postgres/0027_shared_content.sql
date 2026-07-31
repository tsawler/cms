-- Shared regions: {{cmsShared "footer"}} content that every page renders,
-- edited in place like any region but stored once for the whole site.
--
-- The content lives in cms_blocks like everything else, which means it
-- needs a page to hang off. Rather than make page_id nullable — every
-- block query would grow a variant and the UNIQUE key would stop meaning
-- anything — the site gets one reserved page that owns its shared blocks.
-- Sections, locales, draft/published snapshots, and sanitization then work
-- on shared content unchanged, the same trade blog posts make by being
-- pages underneath (see DESIGN.md 4.3, 4.4).
--
-- is_system marks it, so it can be kept out of the admin's Pages list, out
-- of page counts, and off the public site: its slug is unreachable through
-- normal routing anyway (slug validation rejects underscores), but a
-- system page is not a page and nothing should offer it as one.
ALTER TABLE cms_pages ADD COLUMN is_system BOOLEAN NOT NULL DEFAULT false;

-- Published from the start: shared content is always live somewhere, so
-- there is no state in which the site chrome is "not yet published".
INSERT INTO cms_pages (slug, template_name, status, visibility, is_system)
SELECT '__site', '', 'published', 'public', true
WHERE NOT EXISTS (SELECT 1 FROM cms_pages WHERE slug = '__site');
