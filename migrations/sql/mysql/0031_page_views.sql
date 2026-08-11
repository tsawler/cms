-- MySQL/MariaDB port of 0031: daily page-view counters. See the Postgres
-- file for the semantics. The path is VARCHAR rather than TEXT because it
-- is part of the primary key, and MySQL cannot index a TEXT column.
CREATE TABLE cms_page_views (
    day   DATE NOT NULL,
    path  VARCHAR(512) NOT NULL,
    views BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, path)
);
