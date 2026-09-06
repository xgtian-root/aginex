-- +goose Up
CREATE TABLE aginex_rate_limit_windows (
    key_hash CHAR(64) PRIMARY KEY,
    window_started_at_ns BIGINT NOT NULL,
    request_count BIGINT NOT NULL CHECK (request_count >= 0),
    expires_at_ns BIGINT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    insert_token CHAR(32) NOT NULL
);

CREATE INDEX idx_aginex_rate_limit_windows_expires_at
    ON aginex_rate_limit_windows (expires_at_ns);

-- +goose Down
DROP TABLE IF EXISTS aginex_rate_limit_windows;
