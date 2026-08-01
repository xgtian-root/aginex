-- +goose Up
CREATE TABLE aginex_idempotency_records (
    scope_hash TEXT PRIMARY KEY NOT NULL CHECK (length(scope_hash) = 64),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user', 'system', 'service')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 160),
    method TEXT NOT NULL CHECK (length(method) BETWEEN 1 AND 16),
    route TEXT NOT NULL CHECK (length(route) BETWEEN 1 AND 512),
    key_hash TEXT NOT NULL CHECK (length(key_hash) = 64),
    request_digest TEXT NOT NULL CHECK (length(request_digest) = 64),
    state TEXT NOT NULL CHECK (state IN ('in_progress', 'completed')),
    lease_token_hash TEXT NOT NULL DEFAULT '',
    lease_expires_at_ns INTEGER NOT NULL DEFAULT 0,
    response_status INTEGER NOT NULL DEFAULT 0 CHECK (
        response_status = 0 OR response_status BETWEEN 200 AND 599
    ),
    response_content_type TEXT NOT NULL DEFAULT '',
    response_headers BLOB,
    response_body BLOB,
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    expires_at_ns INTEGER NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    CHECK (
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
