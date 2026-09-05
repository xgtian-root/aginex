-- +goose Up
ALTER TABLE role_permissions
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'own'
    CHECK (scope IN ('own', 'all'));

-- +goose Down
ALTER TABLE role_permissions DROP COLUMN scope;
