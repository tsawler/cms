-- A post's header stops being a field and becomes a section.
--
-- header_media_id/header_url held one image that the post template drew
-- above the article, and nothing about how to draw it: no content width, no
-- rounded corners, no say in which part of a tall photo survived being
-- cropped to a banner. Sections already answer all of that, carry a
-- background colour and their own content, and go through the draft/publish
-- flow while doing it. So a post's banner is now an ordinary section, in
-- whatever region the post template names for it, edited with the section
-- gear like every other section on the site.
--
-- Listing cards lose their fallback and use the thumbnail alone, which is
-- the image rendered at card size anyway — the header rendition was
-- full-width, so a card that fell back to it downloaded a banner to show a
-- postage stamp.
--
-- Nothing is carried across: a stored image and a section are different
-- shapes, and no deployment of this CMS has posts to preserve. Header
-- images already chosen are dropped, and the pictures themselves stay in
-- the media library.
ALTER TABLE cms_posts
    DROP COLUMN header_media_id,
    DROP COLUMN header_url;
