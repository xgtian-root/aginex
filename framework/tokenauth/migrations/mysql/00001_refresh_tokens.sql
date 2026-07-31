-- +goose Up
CREATE TABLE aginex_api_refresh_tokens (
    id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    family_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    parent_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    user_id VARCHAR(191) NOT NULL,
    device_id VARCHAR(191) NOT NULL,
    device_name VARCHAR(256) NOT NULL DEFAULT '',
    device_platform VARCHAR(64) NOT NULL DEFAULT '',
    device_metadata TEXT NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    issued_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    used_at DATETIME(6) NULL,
    revoked_at DATETIME(6) NULL,
    revoke_reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    CONSTRAINT chk_aginex_refresh_status CHECK (status IN ('active', 'used', 'revoked')),
    INDEX idx_aginex_refresh_family (family_id),
    INDEX idx_aginex_refresh_user_device (user_id, device_id),
    INDEX idx_aginex_refresh_expiry (expires_at),
    INDEX idx_aginex_refresh_status (status)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS aginex_api_refresh_tokens;
