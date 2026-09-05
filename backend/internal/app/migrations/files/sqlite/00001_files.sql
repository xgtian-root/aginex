-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id TEXT PRIMARY KEY,
    storage_profile_id TEXT,
    provider TEXT NOT NULL,
    bucket TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL UNIQUE,
    original_name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size > 0),
    etag TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    owner_id TEXT NOT NULL REFERENCES users(id),
    visibility TEXT NOT NULL DEFAULT 'private',
    status TEXT NOT NULL DEFAULT 'pending',
    upload_expires_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_file_objects_owner_id ON file_objects(owner_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_storage_profile_id
    ON file_objects(storage_profile_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_status ON file_objects(status);
CREATE INDEX IF NOT EXISTS idx_file_objects_deleted_at ON file_objects(deleted_at);

CREATE TABLE IF NOT EXISTS file_upload_sessions (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL UNIQUE REFERENCES file_objects(id),
    provider_upload_id TEXT NOT NULL,
    resume_fingerprint TEXT NOT NULL CHECK (length(resume_fingerprint) = 64),
    part_size INTEGER NOT NULL CHECK (part_size = 33554432),
    part_count INTEGER NOT NULL CHECK (part_count BETWEEN 2 AND 32),
    status TEXT NOT NULL DEFAULT 'active',
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_file_upload_sessions_status_expires
    ON file_upload_sessions(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_file_upload_sessions_status_updated
    ON file_upload_sessions(status, updated_at);

CREATE TABLE IF NOT EXISTS file_upload_parts (
    session_id TEXT NOT NULL REFERENCES file_upload_sessions(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 32),
    size INTEGER NOT NULL CHECK (size > 0),
    etag TEXT NOT NULL CHECK (length(etag) > 0),
    confirmed_at DATETIME NOT NULL,
    PRIMARY KEY (session_id, part_number)
);

-- +goose Down
CREATE TEMP TABLE aginex_files_down_guard (
    active_sessions INTEGER NOT NULL CHECK (active_sessions = 0)
);
INSERT INTO aginex_files_down_guard (active_sessions)
SELECT COUNT(*)
FROM file_upload_sessions
WHERE status NOT IN ('completed', 'cancelled', 'expired');
DROP TABLE aginex_files_down_guard;
DROP TABLE IF EXISTS file_upload_parts;
DROP TABLE IF EXISTS file_upload_sessions;
DROP TABLE IF EXISTS file_objects;
