-- Whether a post is bylined. See the Postgres file of the same name for why.
ALTER TABLE cms_posts
    ADD COLUMN hide_author BOOLEAN NOT NULL DEFAULT false;
