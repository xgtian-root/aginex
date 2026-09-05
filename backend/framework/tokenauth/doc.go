// Package tokenauth provides an opt-in API access/refresh-token core.
//
// It deliberately does not register HTTP routes, mutate the application's
// primary schema, authenticate passwords, or replace Aginex browser sessions.
// Applications supply identity-provider adapters, explicitly run this
// package's isolated Goose migrations, and decide how token responses are
// transported. IdentityAuthenticator provides the common
// provider/subject-to-local-user boundary; NewGORMIdentityLookup implements it
// for Aginex's standard users and user_identities Goose schema.
//
// Access tokens are short-lived HS256 JWTs with pinned issuer, audience, type,
// subject, expiry, family, and device claims. Refresh tokens are opaque,
// single-use values; only a keyed HMAC is persisted. Reuse of a consumed
// refresh token atomically revokes every active token in that device family.
//
// Family revocation does not maintain a stateless access-token deny-list.
// Already issued access tokens remain valid until their short expiry unless the
// caller's SubjectLookup disables the user. Applications that require immediate
// per-device access revocation must add introspection or a deny-list policy.
//
// Used token rows are replay evidence and must be retained for at least the
// maximum refresh-token lifetime. Refresh expiry is sliding on successful
// rotation; an application that needs a fixed maximum session age should add a
// family-age policy before calling Refresh.
//
// NewBearerMiddleware provides strict bearer parsing, current-subject
// validation, application grant resolution, and module actor context. Login,
// refresh, revocation HTTP operations, rate limits, response cookies/bodies,
// and security audit events remain application-module responsibilities.
// Public credential endpoints must collapse provider, identity-link, and
// subject-status errors into a generic authentication response; the distinct
// package errors exist for control flow and protected operational diagnostics,
// not for account enumeration.
package tokenauth
