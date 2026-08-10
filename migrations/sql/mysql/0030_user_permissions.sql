-- MySQL/MariaDB port of 0030: per-user permission grants. See the
-- Postgres file for the semantics and the backfill rationale.
CREATE TABLE cms_user_permissions (
    user_id    BIGINT NOT NULL,
    permission VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, permission),
    FOREIGN KEY (user_id) REFERENCES cms_users(id) ON DELETE CASCADE
);

INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'blogs' FROM cms_users WHERE role = 'editor';
INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'news' FROM cms_users WHERE role = 'editor';
INSERT INTO cms_user_permissions (user_id, permission)
    SELECT id, 'pages' FROM cms_users WHERE role = 'editor';
