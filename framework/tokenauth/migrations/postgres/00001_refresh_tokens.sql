-- +goose Up
CREATE TABLE aginex_api_refresh_tokens (
    id VARCHAR(64) PRIMARY KEY,
    token_hash CHAR(64) NOT NULL UNIQUE,
    family_id VARCHAR(64) NOT NULL,
    parent_id VARCHAR(64) NULL,
    user_id VARCHAR(191) NOT NULL,
    device_id VARCHAR(191) NOT NULL,
    device_name VARCHAR(256) NOT NULL DEFAULT '',
    device_platform VARCHAR(64) NOT NULL DEFAULT '',
    device_metadata TEXT NOT NULL DEFAULT '{}',
    status VARCHAR(16) NOT NULL CHECK (status IN ('active', 'used', 'revoked')),
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    revoked_at TIMESTAMPTZ NULL,
    revoke_reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_aginex_refresh_family
    ON aginex_api_refresh_tokens (family_id);
CREATE INDEX idx_aginex_refresh_user_device
    ON aginex_api_refresh_tokens (user_id, device_id);
CREATE INDEX idx_aginex_refresh_expiry
    ON aginex_api_refresh_tokens (expires_at);
CREATE INDEX idx_aginex_refresh_status
    ON aginex_api_refresh_tokens (status);

-- +goose Down
DROP TABLE IF EXISTS aginex_api_refresh_tokens;
