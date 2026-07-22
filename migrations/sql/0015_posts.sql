-- Blog & news posts. A post is an ordinary page (its body is blocks and
-- sections, edited in place like any page) plus this row: which feed it
-- belongs to, its display date, its author, and optional listing images.
-- Deleting the backing page deletes the post.
CREATE TABLE cms_posts (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id       BIGINT NOT NULL UNIQUE REFERENCES cms_pages (id) ON DELETE CASCADE,
    feed          TEXT NOT NULL CHECK (feed IN ('blog', 'news')),
    published_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    author_id     BIGINT REFERENCES cms_users (id) ON DELETE SET NULL,
    thumbnail_url TEXT NOT NULL DEFAULT '',
    header_url    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX cms_posts_feed_idx ON cms_posts (feed, published_at DESC);
