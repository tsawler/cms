-- Custom-code blocks: markup plus JavaScript, written into a page by an
-- admin. Pages hold only an inert reference (a <div data-cms-code="key">),
-- so an editor's save can carry the block through the HTML sanitizer
-- without carrying executable content past it; the code itself lives here
-- and is edited behind the admin-only API.
--
-- code_key rather than key: `key` is reserved in MySQL, and the pair of
-- files should read the same.
CREATE TABLE cms_code_snippets (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    code_key   VARCHAR(64) NOT NULL UNIQUE,
    name       VARCHAR(191) NOT NULL,
    html       TEXT NOT NULL DEFAULT (''),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);
