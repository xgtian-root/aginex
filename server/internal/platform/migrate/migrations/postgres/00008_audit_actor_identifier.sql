-- +goose Up
ALTER TABLE audit_logs
    ALTER COLUMN actor_id TYPE VARCHAR(160)
    USING actor_id::text;

-- +goose Down
ALTER TABLE audit_logs
    ALTER COLUMN actor_id TYPE UUID
    USING actor_id::uuid;
