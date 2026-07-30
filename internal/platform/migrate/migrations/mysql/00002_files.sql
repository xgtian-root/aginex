-- +goose Up
CREATE TABLE file_objects (
    id CHAR(36) PRIMARY KEY,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(240) NOT NULL DEFAULT '',
    object_key VARCHAR(700) NOT NULL UNIQUE,
    original_name VARCHAR(500) NOT NULL,
    content_type VARCHAR(160) NOT NULL,
    size BIGINT NOT NULL,
    etag VARCHAR(240) NOT NULL DEFAULT '',
    owner_id CHAR(36) NOT NULL,
    visibility VARCHAR(32) NOT NULL DEFAULT 'private',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_file_objects_owner_id (owner_id),
    CONSTRAINT fk_file_objects_owner FOREIGN KEY (owner_id) REFERENCES users(id)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE file_objects;
