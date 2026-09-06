-- +goose Up
CREATE TABLE aginex_rate_limit_windows (
    key_hash TEXT PRIMARY KEY NOT NULL CHECK (length(key_hash) = 64),
    window_started_at_ns INTEGER NOT NULL,
    request_count INTEGER NOT NULL CHECK (request_count >= 0),
    expires_at_ns INTEGER NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    insert_token TEXT NOT NULL CHECK (length(insert_token) = 32)
);

CREATE INDEX idx_aginex_rate_limit_windows_expires_at
    ON aginex_rate_limit_windows (expires_at_ns);

-- +goose Down
DROP TABLE IF EXISTS aginex_rate_limit_windows;
