CREATE TABLE cms_pages (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    slug          VARCHAR(255) NOT NULL UNIQUE,
    template_name TEXT NOT NULL,
    status        VARCHAR(32) NOT NULL DEFAULT 'draft',
    head_css      TEXT NOT NULL DEFAULT (''),
    body_js       TEXT NOT NULL DEFAULT (''),
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT cms_pages_status_check CHECK (status IN ('draft', 'published'))
);

CREATE TABLE cms_page_meta (
    page_id     BIGINT NOT NULL,
    locale      VARCHAR(16) NOT NULL,
    title       TEXT NOT NULL DEFAULT (''),
    description TEXT NOT NULL DEFAULT (''),
    PRIMARY KEY (page_id, locale),
    -- Named, because 0021 has to drop it to reshape the primary key: InnoDB
    -- keeps the foreign key on the PK's leftmost column and will not let the
    -- index go while a constraint still needs it.
    CONSTRAINT cms_page_meta_page_fk FOREIGN KEY (page_id) REFERENCES cms_pages (id) ON DELETE CASCADE
);

CREATE TABLE cms_blocks (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    page_id     BIGINT NOT NULL,
    region      VARCHAR(64) NOT NULL,
    locale      VARCHAR(16) NOT NULL DEFAULT 'en',
    status      VARCHAR(32) NOT NULL,
    sort        INTEGER NOT NULL DEFAULT 0,
    kind        VARCHAR(32) NOT NULL DEFAULT 'html',
    snippet_key VARCHAR(191),
    content     TEXT NOT NULL DEFAULT (''),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (page_id, region, locale, status, sort),
    CONSTRAINT cms_blocks_page_fk FOREIGN KEY (page_id) REFERENCES cms_pages (id) ON DELETE CASCADE,
    CONSTRAINT cms_blocks_status_check CHECK (status IN ('draft', 'published')),
    CONSTRAINT cms_blocks_kind_check CHECK (kind IN ('text', 'html'))
);

CREATE INDEX cms_blocks_page_idx ON cms_blocks (page_id, locale, status);
