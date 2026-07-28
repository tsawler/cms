-- Page metadata and per-page code join the draft/publish workflow, so that
-- editing a live page no longer changes it on the site before Publish.
--
-- Slug and visibility deliberately stay immediate: a slug change is a move
-- (a staged slug would leave draft and live URLs disagreeing, and the
-- UNIQUE constraint would have to hold across both sets), and visibility is
-- its own control, applied the moment it is set.

-- Metadata mirrors cms_blocks: draft rows are the working copy, published
-- rows are what the public site reads.
ALTER TABLE cms_page_meta ADD COLUMN status VARCHAR(32) NOT NULL DEFAULT 'draft';
ALTER TABLE cms_page_meta ADD CONSTRAINT cms_page_meta_status_check
    CHECK (status IN ('draft', 'published'));

-- The foreign key on page_id rides on the primary key's leftmost column, so
-- InnoDB refuses to drop the index while the constraint exists. Drop it,
-- reshape the key, then put it back.
ALTER TABLE cms_page_meta DROP FOREIGN KEY cms_page_meta_page_fk;
ALTER TABLE cms_page_meta DROP PRIMARY KEY;
ALTER TABLE cms_page_meta ADD PRIMARY KEY (page_id, locale, status);
ALTER TABLE cms_page_meta ADD CONSTRAINT cms_page_meta_page_fk
    FOREIGN KEY (page_id) REFERENCES cms_pages (id) ON DELETE CASCADE;

-- Existing rows were live text with no draft/published distinction. They
-- become the draft working copy (the DEFAULT above), and every page that is
-- currently published also gets a matching published snapshot, so the site
-- keeps serving exactly what it served before this migration.
INSERT INTO cms_page_meta (page_id, locale, title, description, status)
SELECT m.page_id, m.locale, m.title, m.description, 'published'
FROM cms_page_meta m
JOIN cms_pages p ON p.id = m.page_id
WHERE m.status = 'draft' AND p.status = 'published';

-- The page-level fields that are content rather than addressing. cms_pages
-- keeps holding the published values, so the public read path needs no new
-- join; the working copy lives here and is copied over on Publish.
CREATE TABLE cms_page_drafts (
    page_id       BIGINT PRIMARY KEY,
    template_name TEXT NOT NULL,
    head_css      TEXT NOT NULL DEFAULT (''),
    body_js       TEXT NOT NULL DEFAULT (''),
    CONSTRAINT cms_page_drafts_page_fk FOREIGN KEY (page_id) REFERENCES cms_pages (id) ON DELETE CASCADE
);

INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
SELECT id, template_name, head_css, body_js FROM cms_pages;
