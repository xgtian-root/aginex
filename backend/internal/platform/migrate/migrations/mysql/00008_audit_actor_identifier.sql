-- +goose Up
ALTER TABLE audit_logs
    MODIFY COLUMN actor_id VARCHAR(160) NULL;

-- +goose Down
ALTER TABLE audit_logs
    MODIFY COLUMN actor_id CHAR(36) NULL;
