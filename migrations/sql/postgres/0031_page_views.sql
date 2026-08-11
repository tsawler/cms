-- Daily page-view counters for the public site: one row per UTC day and
-- path, incremented by an upsert as pages are served to anonymous
-- visitors. The admin dashboard charts the last seven days; Migrate
-- prunes rows past their retention on every startup, so the table stays
-- a few thousand rows at most.
CREATE TABLE cms_page_views (
    day   DATE NOT NULL,
    path  VARCHAR(512) NOT NULL,
    views BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, path)
);
