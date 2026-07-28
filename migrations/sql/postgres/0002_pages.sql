CREATE TABLE cms_pages (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug          TEXT NOT NULL UNIQUE,
    template_name TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    head_css      TEXT NOT NULL DEFAULT '',
    body_js       TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cms_page_meta (
    page_id     BIGINT NOT NULL REFERENCES cms_pages (id) ON DELETE CASCADE,
    locale      TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (page_id, locale)
);

CREATE TABLE cms_blocks (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id     BIGINT NOT NULL REFERENCES cms_pages (id) ON DELETE CASCADE,
    region      TEXT NOT NULL,
    locale      TEXT NOT NULL DEFAULT 'en',
    status      TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    sort        INTEGER NOT NULL DEFAULT 0,
    kind        TEXT NOT NULL DEFAULT 'html' CHECK (kind IN ('text', 'html')),
    snippet_key TEXT,
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (page_id, region, locale, status, sort)
);

CREATE INDEX cms_blocks_page_idx ON cms_blocks (page_id, locale, status);
