-- +goose Up
CREATE TABLE aginex_idempotency_records (
    scope_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    actor_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_id VARCHAR(160) NOT NULL,
    method VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    route VARCHAR(512) NOT NULL,
    key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    lease_token_hash VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    lease_expires_at_ns BIGINT NOT NULL DEFAULT 0,
    response_status INTEGER NOT NULL DEFAULT 0,
    response_content_type VARCHAR(255) NOT NULL DEFAULT '',
    response_headers BLOB,
    response_body MEDIUMBLOB,
    created_at_ns BIGINT NOT NULL,
    updated_at_ns BIGINT NOT NULL,
    expires_at_ns BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    CONSTRAINT chk_aginex_idempotency_actor_kind CHECK (
        actor_kind IN ('user', 'system', 'service')
    ),
    CONSTRAINT chk_aginex_idempotency_state CHECK (
        state IN ('in_progress', 'completed')
    ),
    CONSTRAINT chk_aginex_idempotency_status CHECK (
        response_status = 0 OR response_status BETWEEN 200 AND 599
    ),
    CONSTRAINT chk_aginex_idempotency_revision CHECK (revision > 0),
    CONSTRAINT chk_aginex_idempotency_lifecycle CHECK (
        (
            state = 'in_progress'
            AND CHAR_LENGTH(lease_token_hash) = 64
            AND lease_expires_at_ns > 0
            AND response_status = 0
        )
        OR (
            state = 'completed'
            AND lease_token_hash = ''
            AND lease_expires_at_ns = 0
            AND response_status BETWEEN 200 AND 599
        )
    ),
    INDEX idx_aginex_idempotency_records_expires_at (expires_at_ns)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS aginex_idempotency_records;
