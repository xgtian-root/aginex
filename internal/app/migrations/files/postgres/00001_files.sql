-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id UUID PRIMARY KEY,
    storage_profile_id UUID,
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
    upload_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_file_objects_owner_id ON file_objects(owner_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_storage_profile_id
    ON file_objects(storage_profile_id);
CREATE INDEX IF NOT EXISTS idx_file_objects_status ON file_objects(status);
CREATE INDEX IF NOT EXISTS idx_file_objects_deleted_at ON file_objects(deleted_at);

CREATE TABLE IF NOT EXISTS file_upload_sessions (
    id UUID PRIMARY KEY,
    file_id UUID NOT NULL UNIQUE REFERENCES file_objects(id),
    provider_upload_id VARCHAR(1024) NOT NULL,
    resume_fingerprint VARCHAR(64) NOT NULL CHECK (char_length(resume_fingerprint) = 64),
    part_size BIGINT NOT NULL CHECK (part_size = 33554432),
    part_count INTEGER NOT NULL CHECK (part_count BETWEEN 2 AND 32),
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_file_upload_sessions_status_expires
    ON file_upload_sessions(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_file_upload_sessions_status_updated
    ON file_upload_sessions(status, updated_at);

CREATE TABLE IF NOT EXISTS file_upload_parts (
    session_id UUID NOT NULL REFERENCES file_upload_sessions(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 32),
    size BIGINT NOT NULL CHECK (size > 0),
    etag VARCHAR(240) NOT NULL CHECK (char_length(etag) > 0),
    confirmed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, part_number)
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM file_upload_sessions
        WHERE status NOT IN ('completed', 'cancelled', 'expired')
    ) THEN
        RAISE EXCEPTION 'disable resumable uploads and drain non-terminal sessions before rollback';
    END IF;
END
$$;
-- +goose StatementEnd
DROP TABLE IF EXISTS file_upload_parts;
DROP TABLE IF EXISTS file_upload_sessions;
DROP TABLE IF EXISTS file_objects;
