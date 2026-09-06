-- +goose Up
CREATE TABLE IF NOT EXISTS products (
    id UUID PRIMARY KEY,
    name VARCHAR(240) NOT NULL,
    sku VARCHAR(120) NOT NULL UNIQUE,
    price_cents BIGINT NOT NULL DEFAULT 0 CHECK (price_cents >= 0),
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
SELECT 1;
