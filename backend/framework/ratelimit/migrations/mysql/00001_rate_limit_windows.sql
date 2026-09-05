-- +goose Up
CREATE TABLE aginex_rate_limit_windows (
    key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    window_started_at_ns BIGINT NOT NULL,
    request_count BIGINT NOT NULL,
    expires_at_ns BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    insert_token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    CONSTRAINT chk_aginex_rate_limit_request_count CHECK (request_count >= 0),
    CONSTRAINT chk_aginex_rate_limit_revision CHECK (revision > 0),
    INDEX idx_aginex_rate_limit_windows_expires_at (expires_at_ns)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS aginex_rate_limit_windows;
