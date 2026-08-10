-- Per-user permission grants: one row grants one permission to one
-- user. Permissions gate editor-role accounts only — the admin and
-- superadmin roles pass every check regardless of this table (see
-- auth.User.Can), so grants stored for them are inert.
--
-- Keys are either the CMS's built-ins (blogs, news, pages, users) or
-- ones a deployment declares in its configuration. A grant whose
-- declaring configuration has been removed is harmless: nothing checks
-- for it anymore.
CREATE TABLE cms_user_permissions (
    user_id    BIGINT NOT NULL REFERENCES cms_users(id) ON DELETE CASCADE,
    permission VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, permission)
);

-- Every existing editor could edit pages, blog, and news before this
-- table existed, so they keep all three. User management was admin-only
-- and stays off.
INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'blogs' FROM cms_users WHERE role = 'editor';
INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'news' FROM cms_users WHERE role = 'editor';
INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'pages' FROM cms_users WHERE role = 'editor';
