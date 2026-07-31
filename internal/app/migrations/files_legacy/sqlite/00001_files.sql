-- +goose Up
ALTER TABLE file_objects ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE file_objects ADD COLUMN width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0);
ALTER TABLE file_objects ADD COLUMN height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0);
ALTER TABLE file_objects ADD COLUMN deleted_at DATETIME;
CREATE INDEX IF NOT EXISTS idx_file_objects_status ON file_objects(status);
CREATE INDEX IF NOT EXISTS idx_file_objects_deleted_at ON file_objects(deleted_at);

-- +goose Down
SELECT 1;
