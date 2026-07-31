// Package idempotency provides a provider-neutral HTTP idempotency lifecycle
// and a portable GORM-backed implementation.
//
// A claim is scoped by actor, HTTP method, registered route template, and a
// client key. The raw client key is never persisted. Exactly one caller
// receives an execution lease; matching concurrent callers observe
// DispositionInProgress, and completed callers receive a bounded replay.
//
// Execution leases must be renewed when work can outlive the configured lease
// duration. An abandoned lease can be recovered after expiry, and the old
// owner can no longer complete it. WithDB lets a successful completion join the
// same GORM transaction as the business write.
//
// State storage is opt-in. Applications must run this package's embedded Goose
// migrations explicitly from their migrate process. API startup only checks
// schema status and never creates or changes these tables.
package idempotency
