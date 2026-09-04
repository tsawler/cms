-- Site search: one row of plain, searchable text per published page and
-- locale, written as Publish goes past.
--
-- A page's words are not in one place. They are spread across cms_blocks —
-- one row per section, per region, per locale, in draft and published
-- copies — as sanitized HTML, with the title and summary off in
-- cms_page_meta. Searching that directly would mean a LIKE across a join
-- with no ranking, no way to strip the markup on the way past (so a search
-- for "hero" would match every section whose wrapper says class="hero"),
-- and no text to cut a result snippet out of.
--
-- So the searchable form of a page is derived once, when the page is
-- published, and kept here: tags stripped, entities decoded, whitespace
-- collapsed. The same trade posts make by being pages (DESIGN.md 4.3) and
-- shared regions make by hanging off __site (4.2.1), pointed the other way
-- — this is the one place where reusing the live tables would have cost
-- more than a derived one.
--
-- The table's contents are its whole security story: a row exists only for
-- a page that is published, public, and not the system page. Nothing is
-- re-checked when the index is queried, so a draft cannot leak through a
-- search result — it is not in here to be found.
CREATE TABLE cms_search_docs (
    page_id BIGINT NOT NULL REFERENCES cms_pages (id) ON DELETE CASCADE,
    locale  TEXT   NOT NULL,
    -- 'page', 'blog' or 'news' — what the result is, so a results template
    -- can group or badge them. It is the post's feed where there is one.
    kind    TEXT   NOT NULL DEFAULT 'page',
    -- The slug as of indexing. Denormalized so a results page costs one
    -- query: a rename reindexes the page, which is cheaper than joining
    -- cms_pages on every search.
    slug    TEXT   NOT NULL DEFAULT '',
    title   TEXT   NOT NULL DEFAULT '',
    -- The page's description: a post's summary, an ordinary page's meta
    -- description. It is both indexed and shown, since a hand-written
    -- summary beats a snippet cut out of the body.
    summary TEXT   NOT NULL DEFAULT '',
    -- Everything else, as text. Long: a page with a dozen sections is a
    -- few tens of kilobytes of prose.
    body    TEXT   NOT NULL DEFAULT '',
    -- A post's display date, for ordering results of equal rank and for
    -- showing beside them. NULL on ordinary pages, which have no such date.
    published_at TIMESTAMPTZ,
    -- The searchable vector, weighted: a hit in the title outranks a hit
    -- in the body, which is the difference between a search for "returns"
    -- finding the returns policy and finding the twelve pages that mention
    -- it in passing.
    --
    -- Not a generated column, deliberately. The text search configuration
    -- varies by locale ('english' for en, 'french' for fr, 'simple' where
    -- the engine has no dictionary for the language), and a generated
    -- column would have to read it out of a column of its own and cast —
    -- and a cast to regconfig is STABLE, not IMMUTABLE, which Postgres
    -- refuses in a generated expression. Writing the vector in the INSERT
    -- takes the configuration as an ordinary parameter and sidesteps the
    -- question; content.Store.reindexPage is the only writer.
    tsv     tsvector NOT NULL,
    PRIMARY KEY (page_id, locale)
);

-- The index the search itself uses. GIN rather than GiST: this table is
-- written on publish and read on every search, which is exactly the ratio
-- GIN is built for.
CREATE INDEX cms_search_docs_tsv_idx ON cms_search_docs USING GIN (tsv);

-- Results are filtered by locale before anything else, so the scan has a
-- starting point on a multilingual site.
CREATE INDEX cms_search_docs_locale_idx ON cms_search_docs (locale);

-- No backfill. The text has to go through the module's own HTML-to-text
-- extraction, which is Go and cannot be spelled in SQL — and doing it
-- badly here would seed the index with markup that the next publish would
-- silently correct. An install that already has content builds the index
-- once at startup instead: see (*cms.CMS).ReindexSearch.
