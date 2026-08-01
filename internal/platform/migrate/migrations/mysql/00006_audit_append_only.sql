-- +goose Up
CREATE TRIGGER audit_logs_prevent_update
BEFORE UPDATE ON audit_logs
FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_logs are append-only';

CREATE TRIGGER audit_logs_prevent_delete
BEFORE DELETE ON audit_logs
FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_logs are append-only';

-- +goose Down
DROP TRIGGER IF EXISTS audit_logs_prevent_delete;
DROP TRIGGER IF EXISTS audit_logs_prevent_update;
