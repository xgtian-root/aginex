-- +goose Up
ALTER TABLE file_objects
    ADD COLUMN sha256 CHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN width INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN height INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN deleted_at DATETIME(6),
    ADD CONSTRAINT chk_file_objects_width CHECK (width >= 0),
    ADD CONSTRAINT chk_file_objects_height CHECK (height >= 0);
CREATE INDEX idx_file_objects_status ON file_objects(status);
CREATE INDEX idx_file_objects_deleted_at ON file_objects(deleted_at);

-- +goose Down
SELECT 1;
