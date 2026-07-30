-- A page's search description stops being the same column as its summary.
--
-- cms_page_meta.description is two things at once: the meta description of
-- an ordinary page, and the summary of a post — the blurb its listing card,
-- its feed entry, and its RSS item all show. Those coincide often enough
-- that one column carried both, but not always: a summary is written for a
-- reader scanning a list of posts, and a meta description for someone
-- reading a search result, and a post has no way to say the two differently.
--
-- meta_description is that second line, stored per locale and staged like
-- the rest of a page's metadata, so a translation carries its own and it
-- reaches the site on the next Publish. Empty means "use description",
-- which is what every existing row says: nothing is migrated, and a page
-- that never sets one keeps publishing exactly the words it does today.
ALTER TABLE cms_page_meta
    ADD COLUMN meta_description TEXT NOT NULL DEFAULT '';
