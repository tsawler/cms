-- Whether a post is bylined.
--
-- Posts have always recorded who wrote them, and templates have always
-- shown that name beside the date. Not every post wants it: a notice, a
-- price change, a release note is published by the site rather than by a
-- person, and the date still matters there while the name does not.
--
-- Stated as "hide" rather than "show" so that false — what every existing
-- row gets, and what any code that forgets the column writes — is the
-- bylined post there has always been. The author itself is untouched, so
-- clearing the flag brings the same name back.
ALTER TABLE cms_posts
    ADD COLUMN hide_author BOOLEAN NOT NULL DEFAULT false;
