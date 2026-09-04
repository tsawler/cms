-- Page history: one frozen copy of a page's published content per edition,
-- so an editor can look at what the site used to say and put it back.
--
-- The draft/published pair in cms_blocks and cms_page_meta gives exactly one
-- step of undo — back to what is live — and nothing before that. Publish is
-- already a whole-page, all-locale snapshot (it deletes the published rows
-- and copies the draft ones over), so a version is that same tuple of data
-- given a third place to live, written as Publish goes past.
--
-- The payload is an opaque JSON document rather than shadow rows, for three
-- reasons. Nothing ever queries *into* a version: it is written once, listed
-- by its metadata, and restored wholesale. A snapshot has to stay readable
-- after the shape of the live tables moves on, which rows pretending to
-- still be blocks would not. And the alternatives are worse — a third value
-- in the status column would require requalifying every `status = 'draft'`
-- in the module, reshaping the UNIQUE key on cms_blocks, and would put
-- history inside the EXCEPT probes in HasUnpublishedChanges.
--
-- What a version deliberately does not carry: slug and visibility. 0021 kept
-- those out of the draft/publish workflow because they are addressing rather
-- than content, and a rollback that silently moved the page's URL would be a
-- nasty surprise. It does carry the custom-code blocks (0032) the page's
-- markup names, since a page holds only an inert placeholder and the body
-- lives elsewhere; restoring puts one back only when the library has lost
-- it, because that library is shared. Media and shared regions are not in
-- it at all — the first lives in the library, the second versions as the
-- __site page's own history. Restoring a version restores the page, not
-- the site.
CREATE TABLE cms_page_versions (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id      BIGINT NOT NULL REFERENCES cms_pages (id) ON DELETE CASCADE,
    -- The snapshot itself: {"v":2,"page":{…},"meta":[…],"blocks":[…],"code":[…]}.
    -- The "v" is the payload format's own number, so an edition stays
    -- readable after the shape moves on; content.decodeSnapshot has the rule.
    -- TEXT rather than JSONB because it is never queried into, and because
    -- the bytes must stay exactly as written for payload_hash to mean
    -- anything; jsonb would normalize whitespace and key order on the way in.
    payload      TEXT NOT NULL,
    -- sha256 of payload, hex. Publishing any page publishes the site's
    -- shared content with it (see DESIGN.md 4.2.1), so without this the
    -- __site page would collect a near-identical version on every single
    -- publish anywhere on the site. A snapshot matching the newest one is
    -- not written at all.
    payload_hash TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT 'publish'
                 CHECK (kind IN ('publish', 'manual')),
    note         TEXT NOT NULL DEFAULT '',
    -- Who published it. SET NULL rather than CASCADE: history outlives the
    -- account that made it, and losing an edition because someone left
    -- would defeat the point of keeping one.
    saved_by     BIGINT REFERENCES cms_users (id) ON DELETE SET NULL,
    saved_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every read is "this page's versions, newest first", and pruning walks the
-- same order. Postgres does not index a foreign key for you, and this index
-- serves that scan too. No DESC: a btree scans backwards just as cheaply.
CREATE INDEX cms_page_versions_page_idx ON cms_page_versions (page_id, id);

-- saved_by is scanned whenever a user is deleted.
CREATE INDEX cms_page_versions_saved_by_idx ON cms_page_versions (saved_by);

-- No backfill. Assembling a payload for every currently-published page would
-- mean building JSON in SQL twice over, once per engine, to produce editions
-- nobody published. History begins at this install's next publish.
