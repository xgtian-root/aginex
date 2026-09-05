-- +goose Up
ALTER TABLE role_permissions
    ADD COLUMN scope VARCHAR(16) NOT NULL DEFAULT 'own',
    ADD CONSTRAINT chk_role_permissions_scope CHECK (scope IN ('own', 'all'));

-- +goose Down
ALTER TABLE role_permissions
    DROP CHECK chk_role_permissions_scope,
    DROP COLUMN scope;
