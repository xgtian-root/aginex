-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    bucket TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL UNIQUE,
    original_name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    owner_id TEXT NOT NULL REFERENCES users(id),
    visibility TEXT NOT NULL DEFAULT 'private',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_file_objects_owner_id ON file_objects(owner_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_status ON file_objects(status);
CREATE INDEX IF NOT EXISTS idx_file_objects_deleted_at ON file_objects(deleted_at);

-- +goose Down
SELECT 1;
