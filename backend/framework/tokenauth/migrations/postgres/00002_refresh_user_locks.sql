-- +goose Up
CREATE TABLE aginex_api_refresh_user_locks (
    user_id VARCHAR(191) PRIMARY KEY,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS aginex_api_refresh_user_locks;
