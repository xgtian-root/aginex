-- +goose Up
CREATE TABLE aginex_api_refresh_tokens (
    id TEXT PRIMARY KEY NOT NULL,
    token_hash TEXT NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    family_id TEXT NOT NULL,
    parent_id TEXT NULL,
    user_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    device_name TEXT NOT NULL DEFAULT '',
    device_platform TEXT NOT NULL DEFAULT '',
    device_metadata TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('active', 'used', 'revoked')),
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    revoked_at DATETIME NULL,
    revoke_reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
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
