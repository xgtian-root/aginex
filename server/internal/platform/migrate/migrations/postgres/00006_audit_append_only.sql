-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION aginex_prevent_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs are append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_logs_prevent_mutation
BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW
EXECUTE FUNCTION aginex_prevent_audit_mutation();

-- +goose Down
DROP TRIGGER IF EXISTS audit_logs_prevent_mutation ON audit_logs;
DROP FUNCTION IF EXISTS aginex_prevent_audit_mutation();
