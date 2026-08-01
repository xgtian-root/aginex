-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

CREATE TABLE users_identity_migration (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);

INSERT INTO users_identity_migration (
    id,
    email,
    display_name,
    password_hash,
    status,
    created_at,
    updated_at,
    deleted_at
)
SELECT
    id,
    email,
    display_name,
    password_hash,
    status,
    created_at,
    updated_at,
    NULL
FROM users;

DROP TABLE users;
ALTER TABLE users_identity_migration RENAME TO users;

CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE user_identities (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    credential_hash TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uq_user_identities_provider_subject UNIQUE (provider, subject)
);

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

PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

UPDATE users
SET password_hash = (
    SELECT identity.credential_hash
    FROM user_identities AS identity
    WHERE identity.user_id = users.id
      AND identity.provider = 'password'
    ORDER BY identity.created_at
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1
    FROM user_identities AS identity
    WHERE identity.user_id = users.id
      AND identity.provider = 'password'
);

DROP TABLE user_identities;

CREATE TABLE users_identity_rollback (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

INSERT INTO users_identity_rollback (
    id,
    email,
    display_name,
    password_hash,
    status,
    created_at,
    updated_at
)
SELECT
    id,
    email,
    display_name,
    COALESCE(password_hash, ''),
    status,
    created_at,
    updated_at
FROM users;

DROP TABLE users;
ALTER TABLE users_identity_rollback RENAME TO users;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd
