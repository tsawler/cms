-- The reader role: accounts that sign in to read private pages and do
-- nothing else. See auth.RoleReader.
ALTER TABLE cms_users DROP CONSTRAINT cms_users_role_check;
ALTER TABLE cms_users ADD CONSTRAINT cms_users_role_check
    CHECK (role IN ('superadmin', 'admin', 'editor', 'reader'));
