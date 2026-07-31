-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER audit_logs_prevent_update
BEFORE UPDATE ON audit_logs
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'audit_logs are append-only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER audit_logs_prevent_delete
BEFORE DELETE ON audit_logs
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'audit_logs are append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS audit_logs_prevent_delete;
DROP TRIGGER IF EXISTS audit_logs_prevent_update;
