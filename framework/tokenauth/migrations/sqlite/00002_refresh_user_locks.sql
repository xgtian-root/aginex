-- +goose Up
CREATE TABLE aginex_api_refresh_user_locks (
    user_id TEXT PRIMARY KEY NOT NULL,
    updated_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS aginex_api_refresh_user_locks;
