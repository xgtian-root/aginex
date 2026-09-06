-- +goose Up
CREATE TABLE aginex_idempotency_records (
    scope_hash CHAR(64) PRIMARY KEY,
    actor_kind VARCHAR(16) NOT NULL CHECK (
        actor_kind IN ('user', 'system', 'service')
    ),
    actor_id VARCHAR(160) NOT NULL,
    method VARCHAR(16) NOT NULL,
    route VARCHAR(512) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    request_digest CHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL CHECK (state IN ('in_progress', 'completed')),
    lease_token_hash VARCHAR(64) NOT NULL DEFAULT '',
    lease_expires_at_ns BIGINT NOT NULL DEFAULT 0,
    response_status INTEGER NOT NULL DEFAULT 0 CHECK (
        response_status = 0 OR response_status BETWEEN 200 AND 599
    ),
    response_content_type VARCHAR(255) NOT NULL DEFAULT '',
    response_headers BYTEA,
    response_body BYTEA,
    created_at_ns BIGINT NOT NULL,
    updated_at_ns BIGINT NOT NULL,
    expires_at_ns BIGINT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    CONSTRAINT chk_aginex_idempotency_lifecycle CHECK (
        (
            state = 'in_progress'
            AND length(lease_token_hash) = 64
            AND lease_expires_at_ns > 0
            AND response_status = 0
        )
        OR (
            state = 'completed'
            AND lease_token_hash = ''
            AND lease_expires_at_ns = 0
            AND response_status BETWEEN 200 AND 599
        )
    )
);

CREATE INDEX idx_aginex_idempotency_records_expires_at
    ON aginex_idempotency_records (expires_at_ns);

-- +goose Down
DROP TABLE IF EXISTS aginex_idempotency_records;
