-- +goose Up
ALTER TABLE users
    MODIFY COLUMN password_hash TEXT NULL,
    ADD COLUMN deleted_at DATETIME(6) NULL;

CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE user_identities (
    id CHAR(36) PRIMARY KEY,
    user_id CHAR(36) NOT NULL,
    provider VARCHAR(64) NOT NULL,
    subject VARCHAR(320) NOT NULL,
    credential_hash TEXT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT uq_user_identities_provider_subject UNIQUE (provider, subject),
    CONSTRAINT fk_user_identities_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE INDEX idx_user_identities_user_id ON user_identities(user_id);

INSERT INTO user_identities (
    id,
    user_id,
    provider,
    subject,
    credential_hash,
    status,
    created_at,
    updated_at
)
SELECT
    id,
    id,
    'password',
    LOWER(TRIM(email)),
    password_hash,
    'active',
    created_at,
    updated_at
FROM users
WHERE password_hash IS NOT NULL
  AND password_hash <> '';

-- +goose Down
UPDATE users
JOIN user_identities AS identity
  ON identity.user_id = users.id
 AND identity.provider = 'password'
SET users.password_hash = identity.credential_hash;

UPDATE users
SET password_hash = ''
WHERE password_hash IS NULL;

DROP TABLE user_identities;
DROP INDEX idx_users_deleted_at ON users;

ALTER TABLE users
    DROP COLUMN deleted_at,
    MODIFY COLUMN password_hash TEXT NOT NULL;
