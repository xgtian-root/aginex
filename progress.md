# Progress

## 2026-07-24

- Confirmed the workspace is empty and is not currently a Git repository.
- Completed market and technical research.
- Locked v1 product decisions and implementation boundaries.
- Created the persistent implementation plan and findings log.
- Created the v1 PRD and third-party dependency catalog under `docs/`.
- Verified the local Go, Node.js, pnpm, Docker, and Git toolchains.
- Loaded the frontend design and Skill authoring requirements that will govern
  the web and Agent Skills phases.
- Initialized the workspace as a Git repository.
- Added the root license, repository metadata, workspace configuration,
  development services, and initial CI workflow.
- Implemented explicit SQLite/PostgreSQL/MySQL Goose migrations.
- Implemented local authentication with Argon2id, revocable database sessions,
  origin checks, explicit RBAC, and audit events.
- Implemented health endpoints and the product reference CRUD with integration
  coverage.
- Built the Next.js 16 administration shell, login, dashboard, products, people,
  access, audit, and files pages; typecheck, Biome, Vitest, and production build
  pass.
- Implemented Local, S3-compatible, and Alibaba OSS v2 storage adapters plus the
  upload-intent, direct-upload, Stat confirmation, signed-read, and delete flow.
- Added OpenAPI snapshot and TypeScript client generation with CI drift checks.
- Added `aginex doctor`, `dev`, `check`, `generate client`, and
  `skills validate`.
- Created and validated nine canonical Skills under `.agents/skills/`.
- Added CI PostgreSQL/MySQL service integration; local execution is pending
  because the Docker daemon was unavailable.

## Next

- Add OIDC Authorization Code + PKCE and identity linking.
- Complete user/role mutation screens and product edit behavior.
- Implement `aginex new`, resource generation, and Agent compatibility install.
- Add Playwright E2E and live MinIO/Alibaba OSS contract environments.
- Add release packaging, security scans, and public English documentation.

## 2026-07-30 Readiness Assessment

- Audited current main across composition, authentication, authorization,
  audit, API contracts, migrations, storage, worker readiness, HTTP security,
  observability, deployment, and CI.
- Confirmed concrete blockers: missing object scope, non-atomic audit, unsafe
  request IDs and redirects, startup migrations, incomplete OpenAPI, unused
  generated client, incomplete file verification/cleanup, and absent durable
  work processing.
- Confirmed existing useful foundations: Argon2id, hashed opaque sessions,
  explicit permissions, Goose migrations for three dialects, storage provider
  interfaces, opaque object keys, health endpoints, HTTP timeouts, and graceful
  shutdown.
- Passed `go test ./...`, `go test -race ./...`, `go vet ./...`,
  `pnpm check:web`, `pnpm test:web`, and `pnpm build:web`.
- PostgreSQL and MySQL local migration subtests skipped because integration DSNs
  were unavailable; only one web unit test existed.
- Kept the worktree clean during assessment.

## 2026-07-31 Remediation

- Began the approved production-hardening implementation.
- Loaded the planning, schema, RBAC, image-upload, and test/debug workflows.
- Reframed the work into core P0 phases plus opt-in token, idempotency, and jobs
  modules while retaining SQLite/PostgreSQL/MySQL portability.
- Added provider-neutral audit event validation and a GORM unit-of-work
  contract.
- Added regression tests proving that audit failure or invalid audit metadata
  rolls back the business mutation; the focused audit/unit-of-work tests pass.
- Migrated product create, update, and delete to the atomic unit of work with
  sanitized before/after event fields. The initial regression reproduced a 201
  response with a committed product after audit failure; it now passes and
  proves the product row is rolled back.
- Added transaction-aware authentication service methods and migrated login and
  logout session mutations to atomic audit. The red regression first showed a
  retained session after audit failure; it now proves the session is rolled
  back.
- Added read-only migration status/version APIs and `aginex migrate
  up|status|version`.
- Replaced API startup migration execution with `EnsureCurrent`; an empty or
  behind database now blocks application construction, while test fixtures run
  migrations explicitly.
- Made OpenAPI composition independent of configuration, database, storage,
  migrations, and bootstrap data. The pure command output is byte-identical to
  the tracked contract snapshot.
- Added bounded Request ID normalization and integrated it into HTTP request
  context handling; malformed client values are replaced before reaching
  responses or audit records.
- Added shared web redirect validation and changed login navigation to reject
  external, protocol, double-slash, backslash, encoded, and control-character
  destinations.
- Focused Go request-ID and Vitest redirect suites pass.
- Added a deterministic, atomic module registry with duplicate detection,
  permission definitions, fail-closed protected-operation metadata, migration
  bundles, optional job handlers, and lifecycle hooks.
- Declared the built-in access, authentication, dashboard, file, health, and
  product modules through the registry and made route mounting consume that
  metadata instead of repeating permissions in the router.
- Added provider-neutral actor, resource reference, object check, and SQL scope
  authorization contracts. Human administrators remain explicit `all` grants;
  `system` is a distinct actor kind rather than an implicit bypass.
- Verified the application/module integration with focused tests and vet:
  `go test ./internal/app -count=1`,
  `go test ./framework/module ./framework/authz -count=1`, and
  `go vet ./internal/app ./framework/module ./framework/authz`.
- Enabled Goose's PostgreSQL session-level advisory locker for explicit
  migration runs. SQLite and MySQL retain their native/Goose migration
  serialization behavior; the PostgreSQL lock is held on the same dedicated
  connection used for SQL migrations.
- Verified the migration-lock package with focused tests and vet using the
  task-scoped Go build cache.
- Added a provider-neutral image verification primitive under
  `framework/storage`. It bounds stream reads, compares claimed MIME to content
  signatures, validates PNG/JPEG/WebP container endings, checks dimensions and
  decoded pixels before full decode, verifies decodability, and derives SHA-256
  plus normalized metadata.
- Added generated PNG/JPEG, WebP fixture, byte-limit, MIME spoofing,
  truncation, corrupt payload, trailing payload, dimension/pixel bomb, policy
  mutation, and cancellation tests. Application handlers and persistence remain
  intentionally unchanged pending the atomic file-lifecycle integration.
- Replaced file-domain serialization in upload intent, confirmation, list, and
  signed-URL handlers with explicit public DTO conversions. Regression coverage
  proves bucket, object key, ETag, owner ID, and future undocumented domain
  fields are not exposed.
- Migrated upload-intent creation and upload confirmation to the transactional
  audit unit of work. Focused regressions now prove that a failed audit insert
  rolls back both new file metadata and the pending-to-ready transition.
- Extended the published audit actor-kind contract to include service actors
  and regenerated OpenAPI plus the typed web client.
- Verified this file-handler slice with `go test ./internal/app -count=1`,
  focused `go test -race`, `go vet ./internal/app`, and `pnpm check:web`.
- Added a provider-neutral shared rate-limit contract and a GORM fixed-window
  implementation with bounded inputs, weighted consumption, precise reset and
  retry metadata, injected clocks, optimistic cross-instance CAS, fail-closed
  corrupt-state handling, and bounded expiry cleanup.
- Added embedded opt-in Goose migrations with a dedicated migration history for
  SQLite, PostgreSQL, and MySQL; API/runtime composition remains unchanged.
- Added deterministic window, weighted cost, raw-key privacy, cleanup,
  validation, database failure, cross-instance, and 96-goroutine contention
  tests. The package test, race test, vet, and whole-framework test suites pass;
  live PostgreSQL/MySQL cases remain conditional on their DSNs.
- Added the provider-neutral durable job runner and registry-backed dispatcher.
  Dispatch is exact on type plus payload version, unknown or panicking handlers
  fail closed, active duplicate claims are coalesced, concurrency is bounded,
  and long-running handlers receive periodic lease heartbeats.
- Propagated enqueue actor, request ID, traceparent, attempt, and idempotency
  metadata through documented handler-context accessors. Cancellation is
  detached only for bounded settlement, so a canceled handler is failed and is
  never falsely marked successful; retry scheduling and dead-state selection
  remain delegated to `Queue.Fail`.
- Verified the job runtime with focused success/retry/dead/unknown/panic,
  cancellation, duplicate-claim, lease-owner, concurrency, and metadata tests:
  `go test ./framework/jobs -count=20`,
  `go test -race ./framework/jobs/... -count=1`, and
  `go vet ./framework/module ./framework/jobs/...`. Live PostgreSQL queue tests
  remain skipped because `AGINEX_TEST_POSTGRES_DSN` is not configured.
- The immediate repository-wide Go check reached and passed both jobs packages
  but remains temporarily red in parallel-owned, unfinished token-auth and file
  cleanup tests; those slices must converge before the final global gate is
  meaningful.
- Completed the durable `storage.cleanup` handler with strict canonical payload
  validation, exact provider/object metadata matching, cleanup-state guards,
  idempotent physical deletion, and an atomic `deleted` transition plus worker
  audit carrying the enqueue actor and request ID. Concurrent completion or
  metadata removal after physical deletion is now treated as terminal success,
  without duplicate completion audits.
- Added SQLite plus Local-storage regressions for successful cleanup, actor and
  trace audit context, duplicate delivery, storage failure, audit-write
  rollback, unsafe states, malformed or mismatched payloads, and both terminal
  races. Verified with `go test ./internal/platform/filecleanup -count=10`,
  `go test -race ./internal/platform/filecleanup -count=1`,
  `go vet ./internal/platform/filecleanup`, and the combined file-cleanup/local
  storage package test.
- Added the provider-neutral opt-in HTTP idempotency contract and portable GORM
  store. Actor/method/route/key scopes are bounded, keys and execution leases
  are hashed at rest, request digests conflict deterministically, concurrent
  callers receive exactly one renewable execution lease, expired leases
  recover, and stale owners cannot complete.
- Added bounded JSON response replay with a non-secret header allowlist,
  recursive sensitive-field and signed-URL rejection, immutable response
  copies, default non-caching of 5xx responses, transactional completion,
  explicit abandon, bounded TTL cleanup, and fail-closed corrupt-state checks.
- Added isolated embedded Goose migrations and read-only schema status for
  SQLite, PostgreSQL, and MySQL. Verified the module with 20 repeated package
  runs, the race detector, vet, SQLite install/rollback/status tests, and a
  96-goroutine contention regression. Live PostgreSQL and MySQL matrix cases
  remain explicitly skipped because their integration DSNs are not configured.
- Added the opt-in `framework/tokenauth` API token core without changing
  browser sessions or application routes. It pins issuer/audience/expiry on
  short-lived HS256 access tokens, stores only separate-key HMACs of opaque
  refresh tokens, carries bounded family/device metadata, and delegates current
  user status to a caller adapter.
- Implemented atomic single-use refresh rotation, replay-triggered family
  revocation, family/device/all-user logout, inactive or deleted-user
  revocation, deterministic clock/entropy injection, and provider-neutral
  identity extension contracts. A failed child insert rolls back consumption.
- Added isolated SQLite/PostgreSQL/MySQL Goose migrations with a dedicated
  history table and read-only schema status. SQLite install, rollback, hash-at-
  rest, deterministic claims, expiry, tampering, scoped revocation, corrupt
  state, rollback, and concurrent replay regressions pass. PostgreSQL and MySQL
  cases explicitly skipped because `AGINEX_TEST_POSTGRES_DSN` and
  `AGINEX_TEST_MYSQL_DSN` are not configured.
- Re-ran the converged Go gates after token-auth integration:
  `go test ./...`, `go test -race ./...`, and `go vet ./...` all pass.
- Separated user subjects from password login identities. Core migration 7
  adds `user_identities` with unique provider/subject records, backfills legacy
  password hashes, adds user soft-deletion metadata, and retains the now-nullable
  `users.password_hash` compatibility column without runtime reads or writes.
- Switched password login and bootstrap to the identity table. Bootstrap now
  creates or reuses the user, password identity, and administrator assignment in
  one transaction, preserves migrated credentials, and supports normalized
  legacy subjects. Unknown or inactive identities and inactive or soft-deleted
  users return one external credential error and perform a bounded dummy Argon2
  verification.
- Added SQLite empty-install, version-6 upgrade, backfill, foreign-key
  preservation, and rollback tests; password-login and atomic/idempotent
  bootstrap regressions; and updated migration/CLI version expectations.
  Verified with the full app package, repeated focused suites, focused race
  tests, and vet across app/auth/password/migrate/file-cleanup/CLI/domain.
  PostgreSQL and MySQL migrations are present; their live DSN matrix remains
  unavailable locally and therefore explicitly unexecuted.
- Integrated the optional idempotency module into application configuration,
  explicit CLI migrations, startup checks, and readiness checks. The default is
  `AGINEX_IDEMPOTENCY_DRIVER=database`; `disabled` remains supported and
  explicitly rejects requests carrying `Idempotency-Key`.
- Added route-level idempotency to product create/update/delete and file upload
  intent/confirmation/deletion. Request bodies are restored after digesting,
  conflicts and active executions return stable RFC Problem responses, replay
  is bounded, and 5xx or other unsuccessful responses abandon their claims.
- Completed replay state inside the same GORM transaction as each normal
  business mutation and successful audit event. Cross-instance, actor
  isolation, actual-path binding, concurrent single-winner, invalid header,
  retry-after-5xx, all six operation, and startup/readiness regressions pass.
- Sanitized upload-intent replay persistence and regenerate signed upload
  requests from authorized pending metadata instead of retaining URL or header
  credentials. OpenAPI idempotency declarations now derive from the same six
  operation IDs that mount runtime enforcement.
- Verified this integration with
  `go test ./internal/app ./internal/config ./internal/cli -count=1`,
  `go test -race ./internal/app ./internal/config ./internal/cli -count=1`,
  `go vet ./internal/app ./internal/config ./internal/cli`, and
  `git diff --check`.
- Added core migration 8 so audit actors are not constrained to UUIDs:
  PostgreSQL and MySQL now store the framework-wide bounded 160-character
  actor identifier, while SQLite records an aligned no-op because its existing
  TEXT column is already unbounded. The domain tag and live-dialect migration
  matrix assert the same width. Focused migration/CLI tests and vet pass.
- Added immutable, build-time `ResourceDefinition` metadata to the module
  registry. A resource must explicitly declare system/owner/custom ownership,
  its policy, non-nil audit and sensitive field classifications, registered
  protected operations, and distinct request/response DTOs. Cross-registration
  validation rejects public or mismatched routes and database-model DTO reuse.
  Repeated module tests, race tests, and vet pass.
- Started the explicit bootstrap boundary: added test-first coverage requiring
  `App.New` to remain data-read-only, repeated bootstrap runs to be safe and
  independently audited, and audit insertion failure to roll back permissions,
  roles, identities, and assignments together.
- Completed the explicit bootstrap boundary. `App.New` no longer invokes any
  seed path; exported `app.Bootstrap` performs a read-only current-schema check,
  derives built-in permissions from the module registry, and atomically
  synchronizes permissions, the Administrator role, an optional password
  identity, role assignments, and one `aginex-bootstrap` CLI/system audit event.
- Added the environment-backed `aginex bootstrap` command and changed the
  README quick start to run `migrate up`, then `bootstrap`, then the API/web
  development processes. The documentation now states that API and worker
  startup only verifies database versions.
- Adapted login/file/session test fixtures to call bootstrap explicitly. The
  regression suite proves startup with configured credentials writes no users,
  identities, permissions, roles, or audits; repeated bootstrap retains one
  administrator while recording every run; missing audit storage rolls all
  bootstrap mutations back; and an unmigrated database is never modified.
- Verified the converged slice with repeated focused app/CLI tests,
  `go test -race ./internal/app ./internal/cli -count=1`,
  `go test ./... -count=1`, `go vet ./...`, and `git diff --check`.
- Added authenticated browser-session self-management as an intrinsic
  `own`-scope capability: every valid principal receives `sessions:read` and
  `sessions:delete` without an application role, while explicit `all` grants
  remain intact.
- Added `DELETE /api/v1/auth/sessions` (`revokeAllSessions`) with owner policy,
  exact `user_id` deletion, an atomic `sessions:revoke-all` audit event, and
  session-cookie expiration only after commit. Ordinary users can revoke all
  of their own devices without affecting administrator or other-user sessions.
- Verified browser-session revocation with ten repeated focused auth/app runs,
  focused race tests, vet, full auth/app package tests, OpenAPI contract
  coverage, audit-failure rollback, unauthenticated denial, Cookie clearing,
  and cross-user isolation.
- Added `storage.cleanup` payload version 2 with exact file/provider/key/mode
  validation. The Worker registers both unchanged version 1 behavior and
  version 2; `explicit-delete` retains the existing deletion lifecycle while
  `pending-expiry` only deletes metadata that is still locked in `pending`.
- Upload-intent creation now atomically enqueues pending expiry for the signed
  request expiry plus a two-minute grace. Pending expiry, explicit deletion,
  and invalid-upload deletion use disjoint idempotency keys so stale no-op jobs
  cannot suppress later cleanup.
- Added expiry regressions for successful deletion, absent objects, stale ready
  and controlled states, exact payload validation, repeated delivery, audit
  rollback, confirmation races, and transaction-bound enqueue/rollback.
  Verified with ten repeated file-cleanup runs, focused app/worker tests,
  focused race tests, vet, and scoped diff checks.
- Completed the executable module boundary. Protected routes now declare
  `all`, actor-derived, object, or query authorization consumption; successful
  responses are held until the matching check/scope has actually been used.
  Owner/custom resource routes cannot register without object/query
  enforcement, and a two-user SQL regression proves list isolation happens in
  the database rather than after retrieval.
- Added strict registered request and structured-success response validation.
  Undeclared response fields are discarded behind a 500 boundary, protected
  OpenAPI operations require matching authentication plus 401/403 contracts,
  and Bearer writes remain exempt from CSRF tokens only when cross-origin
  checks are safe.
- Reworked application lifecycle and server shutdown into bounded state
  machines with independent rollback/drain/cleanup contexts, cached failures,
  re-entry protection, forced HTTP close after a failed drain, and focused
  ordering/cancellation tests.
- Published one `framework/application.Definition` composition root for API,
  worker, migration, bootstrap, and OpenAPI. Migration status reports the
  ordered module fingerprint, while API and worker continue to verify rather
  than mutate schemas.
- Added required/optional, named, individually bounded module readiness checks;
  unversioned `/health/live` and `/health/ready` probes; Local/S3/OSS
  reachability checks; and a worker `Ready(ctx)` decision. External 503
  responses remain generic while internal logs retain the failed check name.
- Published provider-neutral runtime services to request, lifecycle,
  readiness, and worker job contexts: GORM, atomic audited writes, the object
  store, optional transactional jobs, and the shared observability recorder.
  External-module tests prove handlers and lifecycle hooks can extend Aginex
  without importing `internal` packages or using globals.
- Added vendor-neutral observability with W3C extraction/propagation,
  server/client/producer/consumer/internal spans, immutable bounded
  attributes, and counter/histogram/gauge points. HTTP, all six GORM callback
  classes, SQL pool state, PostgreSQL jobs, shared rate limits, and
  Local/S3/OSS storage now use low-cardinality instrumentation that excludes
  SQL, parameters, actors, IDs, payloads, limiter keys, object metadata,
  signed URLs, and provider errors.
- Transactional enqueue now persists its producer span context and the worker
  restores it as a consumer parent. Failure settlement detaches cancellation
  without dropping trace context. Jobs expose claim, handler, heartbeat,
  settlement, and active metrics; storage observation transparently preserves
  readiness and Local upload/content adapters.
- Documented the framework/application boundary, deployment-owned telemetry
  exporters, production probes, and the deliberately breaking pre-stable
  upgrade path. R1 through R6 are complete; R7 remains in progress until the
  final repository, web, contract, security, image, and available live-provider
  gates converge.
- Bound the declared upload size into S3 and OSS V4 presigned PUT requests.
  Offline signer regressions assert `Content-Length` is returned as a required
  signed header, and the live S3/OSS contract now asserts the same provider
  contract before uploading.
- Added a production configuration gate that rejects disabled durable jobs
  when the selected composition includes file upload/delete cleanup. Focused
  storage and configuration suites plus `go test ./... -count=1` pass; live
  cloud contracts remain environment-gated when provider credentials are
  unavailable.

## 2026-07-31 Final Remediation Closeout

- Made the framework definition truly business-free by default. Files are an
  official opt-in module; Product and Dashboard are starter examples. The
  repository reference composition selects those modules explicitly, while a
  zero definition creates no product/file tables and exposes none of their
  routes.
- Split bundled migrations into independently versioned module histories,
  added pre-stable adoption and partial-schema rejection, and made API, Worker,
  migrate, bootstrap, and OpenAPI consume one definition and fingerprint.
- Finished the production security pass: canonical browser cookie/CSRF
  protocol, typed OpenAPI CSRF headers, generated-client integration, strict
  production secrets/bootstrap passwords, trusted-proxy rejection, and
  application-level startup regressions.
- Re-ran the final local gates successfully:
  `go test ./... -count=1`, `go test -race ./... -count=1`,
  `go vet ./...`, `go mod verify`, `pnpm check:web`, `pnpm test:web`,
  `pnpm build:web`, Playwright test discovery, local storage contracts, and
  all nine Skill validations.
- Regenerated OpenAPI and the web client twice with identical SHA-256 outputs:
  `ecb0b9867dc95f6f143e1c49ed9f9d3110ed5208798776f1d4819859099728e3`
  and `38d1c60837f2cabcb24ee842ee79cc9684481454e11061fb92e08a4f7d5ca21f`.
- Recorded unavailable evidence explicitly: live PostgreSQL/MySQL module and
  core migration matrices lacked DSNs; live S3/OSS lacked credentials; the
  Docker daemon was unavailable; real Playwright E2E and local security/SBOM
  binaries were unavailable. CI contains these gates, so the source is ready
  for a new pre-release but stable publication remains blocked until they pass
  in the release environment.
