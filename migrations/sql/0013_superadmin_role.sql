-- The superadmin role: users trusted with raw HTML access in the
-- in-place editor (the whole-page source view). A superset of admin.
ALTER TABLE cms_users DROP CONSTRAINT cms_users_role_check;
ALTER TABLE cms_users ADD CONSTRAINT cms_users_role_check
    CHECK (role IN ('superadmin', 'admin', 'editor'));
