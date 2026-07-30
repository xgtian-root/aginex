-- +goose Up
CREATE TABLE file_objects (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    bucket TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL UNIQUE,
    original_name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    owner_id TEXT NOT NULL REFERENCES users(id),
    visibility TEXT NOT NULL DEFAULT 'private',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_file_objects_owner_id ON file_objects(owner_id);

-- +goose Down
DROP TABLE file_objects;
