-- +goose Up
ALTER TABLE audit_logs
    ADD COLUMN actor_kind VARCHAR(32) NOT NULL DEFAULT 'user',
    ADD COLUMN result VARCHAR(32) NOT NULL DEFAULT 'success',
    ADD COLUMN source VARCHAR(64) NOT NULL DEFAULT 'http',
    ADD COLUMN sanitized_before JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN sanitized_after JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE audit_logs
SET actor_kind = 'system'
WHERE actor_id IS NULL;

CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_kind, actor_id, created_at DESC);
CREATE INDEX idx_audit_logs_resource ON audit_logs(resource, resource_id, created_at DESC);
CREATE INDEX idx_audit_logs_request_id ON audit_logs(request_id);

-- +goose Down
DROP INDEX idx_audit_logs_request_id;
DROP INDEX idx_audit_logs_resource;
DROP INDEX idx_audit_logs_actor;

ALTER TABLE audit_logs
    DROP COLUMN sanitized_after,
    DROP COLUMN sanitized_before,
    DROP COLUMN source,
    DROP COLUMN result,
    DROP COLUMN actor_kind;
