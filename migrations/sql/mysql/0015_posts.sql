-- Blog & news posts. A post is an ordinary page (its body is blocks and
-- sections, edited in place like any page) plus this row: which feed it
-- belongs to, its display date, its author, and optional listing images.
-- Deleting the backing page deletes the post.
CREATE TABLE cms_posts (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    page_id       BIGINT NOT NULL UNIQUE,
    feed          VARCHAR(32) NOT NULL,
    published_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    author_id     BIGINT,
    thumbnail_url TEXT NOT NULL DEFAULT (''),
    header_url    TEXT NOT NULL DEFAULT (''),
    FOREIGN KEY (page_id) REFERENCES cms_pages (id) ON DELETE CASCADE,
    FOREIGN KEY (author_id) REFERENCES cms_users (id) ON DELETE SET NULL,
    CONSTRAINT cms_posts_feed_check CHECK (feed IN ('blog', 'news'))
);

-- MariaDB parses DESC in an index definition but ignores it; the ordering
-- still comes from the query's ORDER BY either way.
CREATE INDEX cms_posts_feed_idx ON cms_posts (feed, published_at DESC);
