-- +goose Up
ALTER TABLE audit_logs ADD COLUMN actor_kind TEXT NOT NULL DEFAULT 'user';
ALTER TABLE audit_logs ADD COLUMN result TEXT NOT NULL DEFAULT 'success';
ALTER TABLE audit_logs ADD COLUMN source TEXT NOT NULL DEFAULT 'http';
ALTER TABLE audit_logs ADD COLUMN sanitized_before TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(sanitized_before));
ALTER TABLE audit_logs ADD COLUMN sanitized_after TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(sanitized_after));

UPDATE audit_logs
SET actor_kind = 'system'
WHERE actor_id IS NULL;

CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_kind, actor_id, created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs(resource, resource_id, created_at);
CREATE INDEX idx_audit_logs_request_id ON audit_logs(request_id);

-- +goose Down
DROP INDEX idx_audit_logs_request_id;
DROP INDEX idx_audit_logs_resource;
DROP INDEX idx_audit_logs_actor;

ALTER TABLE audit_logs DROP COLUMN sanitized_after;
ALTER TABLE audit_logs DROP COLUMN sanitized_before;
ALTER TABLE audit_logs DROP COLUMN source;
ALTER TABLE audit_logs DROP COLUMN result;
ALTER TABLE audit_logs DROP COLUMN actor_kind;
