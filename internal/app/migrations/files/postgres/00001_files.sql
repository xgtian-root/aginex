-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id UUID PRIMARY KEY,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(240) NOT NULL DEFAULT '',
    object_key VARCHAR(700) NOT NULL UNIQUE,
    original_name VARCHAR(500) NOT NULL,
    content_type VARCHAR(160) NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    etag VARCHAR(240) NOT NULL DEFAULT '',
    sha256 CHAR(64) NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    owner_id UUID NOT NULL REFERENCES users(id),
    visibility VARCHAR(32) NOT NULL DEFAULT 'private',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_file_objects_owner_id ON file_objects(owner_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_status ON file_objects(status);
CREATE INDEX IF NOT EXISTS idx_file_objects_deleted_at ON file_objects(deleted_at);

-- +goose Down
SELECT 1;
