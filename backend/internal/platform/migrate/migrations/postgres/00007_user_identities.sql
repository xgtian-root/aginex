-- +goose Up
ALTER TABLE users
    ALTER COLUMN password_hash DROP NOT NULL,
    ADD COLUMN deleted_at TIMESTAMPTZ;

CREATE INDEX idx_users_deleted_at ON users(deleted_at);

CREATE TABLE user_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(64) NOT NULL,
    subject VARCHAR(320) NOT NULL,
    credential_hash TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
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
    LOWER(BTRIM(email)),
    password_hash,
    'active',
    created_at,
    updated_at
FROM users
WHERE password_hash IS NOT NULL
  AND password_hash <> '';

-- +goose Down
UPDATE users
SET password_hash = identity.credential_hash
FROM user_identities AS identity
WHERE identity.user_id = users.id
  AND identity.provider = 'password';

UPDATE users
SET password_hash = ''
WHERE password_hash IS NULL;

DROP TABLE user_identities;
DROP INDEX idx_users_deleted_at;

ALTER TABLE users
    DROP COLUMN deleted_at,
    ALTER COLUMN password_hash SET NOT NULL;
