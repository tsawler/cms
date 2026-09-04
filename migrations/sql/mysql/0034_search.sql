-- MySQL/MariaDB port of 0034: the derived site-search index. See the
-- Postgres file for why a page's searchable text is derived at publish
-- time rather than queried out of cms_blocks, and for the invariant that
-- only published, public, non-system pages have rows here.
--
-- Two differences that are not mechanical.
--
-- There is no tsvector column. Both engines carry the searchable form
-- inside the FULLTEXT index itself rather than in a column of their own,
-- so the words are matched straight out of title, summary and body — which
-- also means there is nothing to keep in step with them, and no text
-- search configuration to store. The cost is that the per-locale
-- dictionary Postgres gets is not available here; both engines stem in one
-- way for every language.
--
-- body is LONGTEXT for the same reason cms_page_versions.payload is: TEXT
-- holds 64KB, and the whole prose of a page with a dozen sections goes
-- past that without being an unusual page. A truncated body would index
-- silently less than the page says.
CREATE TABLE cms_search_docs (
    page_id      BIGINT NOT NULL,
    locale       VARCHAR(16) NOT NULL,
    kind         VARCHAR(32) NOT NULL DEFAULT 'page',
    slug         VARCHAR(255) NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT (''),
    summary      TEXT NOT NULL DEFAULT (''),
    body         LONGTEXT NOT NULL DEFAULT (''),
    published_at DATETIME(6) NULL,
    PRIMARY KEY (page_id, locale),
    CONSTRAINT cms_search_docs_page_fk FOREIGN KEY (page_id)
        REFERENCES cms_pages (id) ON DELETE CASCADE,
    -- The index the search itself uses. One FULLTEXT index over the three
    -- columns, because MATCH() must name exactly the columns some index
    -- was built on: three separate indexes could not answer one query.
    -- Ranking the title above the body is done in the query instead, by
    -- adding a second MATCH against the title alone to the score.
    FULLTEXT KEY cms_search_docs_ft (title, summary, body),
    -- And the title on its own, which is what makes the title outrank the
    -- body: the query adds a second MATCH against this index to the score.
    -- Postgres spells the same idea as setweight() on one vector.
    FULLTEXT KEY cms_search_docs_ft_title (title)
);

-- Results are filtered by locale before anything else.
CREATE INDEX cms_search_docs_locale_idx ON cms_search_docs (locale);

-- No backfill, for the reason the Postgres file gives: the text goes
-- through the module's Go HTML-to-text extraction, which SQL cannot spell.
