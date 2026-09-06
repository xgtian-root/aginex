// Package ratelimit provides a shared-state, provider-neutral rate-limiter
// contract and a portable GORM implementation.
//
// GORMLimiter uses fixed, epoch-aligned windows and optimistic compare-and-swap
// updates. The application service selects a namespace, key, limit, cost, and
// window without inspecting the database dialect. Raw keys are validated,
// bounded, and SHA-256 hashed before persistence.
//
// State storage is opt-in. Applications that enable this package must run the
// package migrations explicitly with NewMigrationProvider during their migrate
// process. API and worker startup must not run these migrations automatically.
// The embedded SQLite, PostgreSQL, and MySQL migrations use a dedicated Goose
// version table so they do not collide with application-owned migrations.
package ratelimit
