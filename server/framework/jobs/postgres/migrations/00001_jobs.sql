-- +goose Up
CREATE TABLE aginex_jobs (
    id UUID PRIMARY KEY,
    type VARCHAR(120) NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    payload JSONB NOT NULL,
    payload_sha256 CHAR(64) NOT NULL,
    idempotency_key VARCHAR(200) NOT NULL,
    state VARCHAR(16) NOT NULL CHECK (
        state IN ('pending', 'running', 'succeeded', 'failed', 'dead')
    ),
    scheduled_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    locked_by VARCHAR(128),
    locked_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_by_kind VARCHAR(16) NOT NULL CHECK (
        created_by_kind IN ('user', 'system')
    ),
    created_by_id VARCHAR(160) NOT NULL,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    traceparent VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT uq_aginex_jobs_idempotency UNIQUE (type, idempotency_key)
);

CREATE INDEX idx_aginex_jobs_claim
    ON aginex_jobs (state, scheduled_at, created_at);

CREATE INDEX idx_aginex_jobs_stale_lease
    ON aginex_jobs (heartbeat_at)
    WHERE state = 'running';

CREATE INDEX idx_aginex_jobs_dead
    ON aginex_jobs (updated_at)
    WHERE state = 'dead';

-- +goose Down
DROP TABLE IF EXISTS aginex_jobs;
