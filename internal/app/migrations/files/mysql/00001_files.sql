-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id CHAR(36) PRIMARY KEY,
    storage_profile_id CHAR(36) NULL,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(240) NOT NULL DEFAULT '',
    object_key VARCHAR(700) NOT NULL UNIQUE,
    original_name VARCHAR(500) NOT NULL,
    content_type VARCHAR(160) NOT NULL,
    size BIGINT NOT NULL,
    etag VARCHAR(240) NOT NULL DEFAULT '',
    sha256 CHAR(64) NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    owner_id CHAR(36) NOT NULL,
    visibility VARCHAR(32) NOT NULL DEFAULT 'private',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    upload_expires_at DATETIME(6),
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    deleted_at DATETIME(6),
    INDEX idx_file_objects_owner_id (owner_id),
    INDEX idx_file_objects_storage_profile_id (storage_profile_id),
    INDEX idx_file_objects_status (status),
    INDEX idx_file_objects_deleted_at (deleted_at),
    CONSTRAINT fk_file_objects_owner FOREIGN KEY (owner_id) REFERENCES users(id),
    CONSTRAINT chk_file_objects_size CHECK (size > 0),
    CONSTRAINT chk_file_objects_width CHECK (width >= 0),
    CONSTRAINT chk_file_objects_height CHECK (height >= 0)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS file_upload_sessions (
    id CHAR(36) PRIMARY KEY,
    file_id CHAR(36) NOT NULL,
    provider_upload_id VARCHAR(1024) NOT NULL,
    resume_fingerprint VARCHAR(64) NOT NULL,
    part_size BIGINT NOT NULL,
    part_count INT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    expires_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY uq_file_upload_sessions_file_id (file_id),
    INDEX idx_file_upload_sessions_status_expires (status, expires_at),
    INDEX idx_file_upload_sessions_status_updated (status, updated_at),
    CONSTRAINT fk_file_upload_sessions_file
        FOREIGN KEY (file_id) REFERENCES file_objects(id),
    CONSTRAINT chk_file_upload_sessions_fingerprint CHECK (CHAR_LENGTH(resume_fingerprint) = 64),
    CONSTRAINT chk_file_upload_sessions_part_size CHECK (part_size = 33554432),
    CONSTRAINT chk_file_upload_sessions_part_count CHECK (part_count BETWEEN 2 AND 32)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS file_upload_parts (
    session_id CHAR(36) NOT NULL,
    part_number INT NOT NULL,
    size BIGINT NOT NULL,
    etag VARCHAR(240) NOT NULL,
    confirmed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (session_id, part_number),
    CONSTRAINT fk_file_upload_parts_session
        FOREIGN KEY (session_id) REFERENCES file_upload_sessions(id) ON DELETE CASCADE,
    CONSTRAINT chk_file_upload_parts_number CHECK (part_number BETWEEN 1 AND 32),
    CONSTRAINT chk_file_upload_parts_size CHECK (size > 0),
    CONSTRAINT chk_file_upload_parts_etag CHECK (CHAR_LENGTH(etag) > 0)
) ENGINE=InnoDB;

-- +goose Down
CREATE TEMPORARY TABLE aginex_files_down_guard (
    active_sessions INT NOT NULL,
    CONSTRAINT chk_aginex_files_down_guard CHECK (active_sessions = 0)
);
INSERT INTO aginex_files_down_guard (active_sessions)
SELECT COUNT(*)
FROM file_upload_sessions
WHERE status NOT IN ('completed', 'cancelled', 'expired');
DROP TEMPORARY TABLE aginex_files_down_guard;
DROP TABLE IF EXISTS file_upload_parts;
DROP TABLE IF EXISTS file_upload_sessions;
DROP TABLE IF EXISTS file_objects;
