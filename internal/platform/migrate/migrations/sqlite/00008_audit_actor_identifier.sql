-- +goose Up
-- SQLite already stores actor_id as unbounded TEXT. This version keeps the
-- cross-dialect schema history aligned without rebuilding audit_logs, which
-- would temporarily remove its append-only triggers.
SELECT 1;

-- +goose Down
SELECT 1;
