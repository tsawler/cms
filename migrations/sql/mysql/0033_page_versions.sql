-- MySQL/MariaDB port of 0033: page history. See the Postgres file for what a
-- version is, what it deliberately leaves out, and why the payload is an
-- opaque document rather than shadow rows.
--
-- The one difference that is not mechanical is the payload's type. TEXT here
-- holds 64KB, and a page's whole published content — every region, every
-- locale — passes that routinely; cms_blocks.content is itself a TEXT column,
-- so a page needs only a couple of full regions to overflow one. LONGTEXT is
-- the column that can hold what this table exists to hold.
CREATE TABLE cms_page_versions (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    page_id      BIGINT NOT NULL,
    payload      LONGTEXT NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    kind         VARCHAR(32) NOT NULL DEFAULT 'publish',
    note         TEXT NOT NULL DEFAULT (''),
    saved_by     BIGINT NULL,
    saved_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT cms_page_versions_page_fk FOREIGN KEY (page_id)
        REFERENCES cms_pages (id) ON DELETE CASCADE,
    CONSTRAINT cms_page_versions_user_fk FOREIGN KEY (saved_by)
        REFERENCES cms_users (id) ON DELETE SET NULL,
    CONSTRAINT cms_page_versions_kind_check CHECK (kind IN ('publish', 'manual'))
);

-- No explicit indexes: InnoDB creates one for each foreign key, and a
-- secondary index carries the clustered primary key as its suffix — so the
-- index on page_id is already the (page_id, id) the Postgres file spells out,
-- which is the order every read and the pruning walk want.
