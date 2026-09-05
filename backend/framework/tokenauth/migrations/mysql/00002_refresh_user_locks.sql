-- +goose Up
CREATE TABLE aginex_api_refresh_user_locks (
    user_id VARCHAR(191) NOT NULL PRIMARY KEY,
    updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS aginex_api_refresh_user_locks;
