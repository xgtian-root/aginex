-- +goose Up
CREATE TABLE IF NOT EXISTS file_objects (
    id CHAR(36) PRIMARY KEY,
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
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    deleted_at DATETIME(6),
    INDEX idx_file_objects_owner_id (owner_id),
    INDEX idx_file_objects_status (status),
    INDEX idx_file_objects_deleted_at (deleted_at),
    CONSTRAINT fk_file_objects_owner FOREIGN KEY (owner_id) REFERENCES users(id),
    CONSTRAINT chk_file_objects_width CHECK (width >= 0),
    CONSTRAINT chk_file_objects_height CHECK (height >= 0)
) ENGINE=InnoDB;

-- +goose Down
SELECT 1;
