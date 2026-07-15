CREATE TABLE cms_menu_items (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    menu      TEXT NOT NULL DEFAULT 'main',
    sort      INTEGER NOT NULL DEFAULT 0,
    label     TEXT NOT NULL,
    -- An item links either to a page (URL resolved from the slug at render
    -- time, item removed when the page is deleted) or to a literal URL.
    page_id   BIGINT REFERENCES cms_pages (id) ON DELETE CASCADE,
    url       TEXT NOT NULL DEFAULT '',
    new_tab   BOOLEAN NOT NULL DEFAULT false,
    -- Reserved for nested menus; unused by the v1 UI.
    parent_id BIGINT REFERENCES cms_menu_items (id) ON DELETE CASCADE
);

CREATE INDEX cms_menu_items_menu_idx ON cms_menu_items (menu, sort);
