# Findings

## Market

- `gin-vue-admin` and `go-admin` validate demand for batteries-included Go
  admin frameworks, but are Vue-first and do not provide a portable,
  measurable Coding Agent workflow.
- Refine and React Admin validate demand for typed, composable React admin
  primitives, but do not provide an integrated Go backend contract.
- AdminForth uses "Agent-first" for runtime business agents. Aginex must
  consistently describe itself as Coding-Agent-first development
  infrastructure.
- Agent Skills is an open specification with progressive disclosure through
  `SKILL.md`, `references/`, `scripts/`, and `assets/`.

## Architecture

- Gin remains the HTTP router and middleware host.
- Huma v2 can use the official Gin adapter and generate OpenAPI 3.1/3.0 from Go
  input/output types, reducing annotation and client drift.
- GORM is selected for ecosystem familiarity and multi-database support.
- Goose is selected for explicit, reviewable production migrations; GORM
  `AutoMigrate` must not run in production.
- RBAC will use explicit relational tables and `resource:action` identifiers in
  v1. Casbin is deferred until ABAC or data-scope requirements justify it.
- Browser authentication uses revocable server-side sessions and secure
  cookies, not browser-stored JWTs.

## Dependency Risks

- The default SQLite path should avoid CGO for easy local onboarding.
  `github.com/glebarez/sqlite` is the planned GORM dialector, but it must pass
  the Go 1.25/1.26 and full GORM behavior matrix before release. The official
  CGO driver remains an opt-in fallback.
- Alibaba Cloud OSS must use the official v2 SDK rather than the legacy SDK.
- S3-compatible providers share the AWS SDK v2 adapter; a separate MinIO SDK is
  unnecessary for v1.

## Scope Boundaries

- No multi-tenancy, dynamic database menus, visual low-code builder, workflow
  engine, plugin marketplace, image transformations, or runtime LLM agent in
  v1.
- English is the product language. These planning documents are Chinese-first
  so product decisions can be reviewed efficiently; public guides can be
  translated after the implementation stabilizes.

## Production Readiness Assessment (2026-07-30)

- The repository is a functioning early vertical slice, but its current green
  checks do not establish multi-user production safety.
- `App.New` currently applies migrations, bootstraps data, constructs storage,
  and registers all routes. Composition, migration, and contract generation
  need independent side-effect-free entry points.
- File metadata already has an owner, but list, signed-read, local-content,
  local-upload, and delete paths do not consistently enforce ownership.
- Successful business writes and audit inserts use separate transactions; audit
  failure currently logs and allows the business write to remain committed.
- Most business routes are registered with Gin and manually mirrored into
  OpenAPI without request, response, error, query, or security schemas. The
  generated TypeScript client is not the frontend's actual contract.
- Opaque browser session tokens are random and hashed at rest, but CSRF,
  redirect validation, proxy trust, request limits, shared rate limiting, and
  production configuration checks are incomplete.
- The storage abstraction, opaque keys, size/type policy, and path validation
  are useful foundations. Deep image verification, checksum, reliable cleanup,
  and live provider contracts are missing.
- A PostgreSQL-only queue cannot be a framework invariant while SQLite and
  MySQL remain supported. Durable jobs belong behind an opt-in module/provider
  contract; POSTA may select the PostgreSQL provider.
- The nested `{error:{...}}` proposal conflicts with RFC Problem Details, and a
  universal cursor-page switch conflicts with the existing admin list contract.

## Remediation Architecture

- Core packages will expose compile-time module registration, typed operations,
  actor/authorizer contracts, transactional audit, migration version checks,
  safe HTTP middleware, and storage lifecycle primitives.
- Optional packages will provide public token authentication, idempotency, and
  durable job processing without forcing their tables or processes into every
  application.
- POSTA owns SMS/Apple adapters, domain resources, custom ownership rules,
  quota values, abuse thresholds, task payloads, and operational retention
  policies.
- The framework delivery model must be made explicit. Until reusable public Go
  packages or `aginex new` exist, a second application can only fork/copy the
  repository despite the documented module goal.
- The existing handlers can migrate incrementally to atomic audit without
  changing database dialect behavior: a GORM unit of work accepts a mutation
  that returns its sanitized audit event, records that event through the same
  transaction handle, and commits only after both succeed.
- Authentication service methods currently own their database handle directly.
  Atomic login/logout audit will require transaction-aware service methods or a
  repository method that accepts the transaction supplied by the unit of work.
- Only `internal/app` calls `auth.Service.Login` and `Logout`, so adding
  transaction-aware variants can preserve compatibility wrappers without
  affecting another package API.
- The new module registry establishes fail-closed operation metadata and
  deterministic composition, but its operation definition intentionally does
  not yet carry Go request/response DTO types. Typed Huma binding remains an R3
  integration task rather than being implied by the registry alone.
- Migration status/version inspection is now read-only on an empty database and
  uses an instance Goose provider instead of process-global dialect/FS state.
  Cross-process migration locks remain outstanding and must be implemented per
  supported adapter without leaking dialect logic into services.
- The authorization package keeps `system` as an actor kind and requires
  explicit system permissions; no human administrator receives an implicit
  system bypass.
- Built-in HTTP routes can now be mounted from registered operation metadata,
  which removes permission drift at the router boundary. Object/query policy
  enforcement is deliberately still a separate runtime step and remains part
  of R2.
- Registry composition is side-effect-free and reusable by OpenAPI generation;
  the current OpenAPI document still manually describes most Gin operations,
  so the registry alone does not satisfy the typed-contract R3 gate.
- Goose 3.26 already exposes a PostgreSQL session locker and runs SQL
  migrations on the lock-owning dedicated connection. Using that provider
  option is safer than maintaining a parallel advisory-lock implementation.
- Safe image verification can remain provider-neutral: a bounded reader,
  signature-derived MIME type, format container checks, decode-config limits,
  full decoding, and content hashing do not require a cloud SDK or database.
  The new primitive deliberately remains separate from upload handlers until
  the file lifecycle can persist all verified metadata atomically.
- File API contracts must be projections rather than JSON-tagged persistence
  models. The public file projection intentionally omits bucket, object key,
  ETag, and owner ID, while signed upload/read data has its own narrow response
  contract.
- Object-store verification should remain outside the database transaction, but
  confirmation must re-read and re-authorize the intent before atomically
  persisting verified metadata and its audit event. This avoids holding a
  database transaction across remote I/O without trusting stale intent fields.
- A portable shared limiter does not need service-layer dialect branches:
  storing a revision with each fixed window permits compare-and-swap updates,
  while GORM renders the duplicate-safe insert for SQLite, PostgreSQL, and
  MySQL. An immutable random insert token avoids trusting dialect-dependent
  `RowsAffected` behavior on duplicate inserts. Denials are fail-closed and
  never increment the counter.
- Rate-limit state belongs to an opt-in module rather than the core schema.
  Embedded dialect migrations use their own Goose history table, so enabling
  or removing the module cannot advance the application's schema version.
- Persisting only a bounded SHA-256 bucket identifier keeps raw IP, email, or
  account keys out of the shared state table. Namespace, subject key, and
  window duration define independent buckets; application projects still own
  concrete thresholds and abuse policy.
- Durable job dispatch can consume the module registry without a package cycle:
  `module.JobHandler` deliberately depends only on `context` and JSON, while
  the runtime edge in `framework/jobs` imports the immutable registry snapshot.
  Retry timing remains provider-owned through `Queue.Fail`; the runner owns
  exact type/version selection, lease heartbeats, bounded execution, and
  fail-closed settlement.
- Portable HTTP idempotency does not require dialect-specific service logic:
  a SHA-256 scope over actor, method, route, and key plus revisioned
  compare-and-swap transitions grants one execution lease across SQLite,
  PostgreSQL, and MySQL. Lease capabilities and client keys are hashed at rest,
  while expiry permits bounded recovery without accepting a stale completion.
- Safe replay is intentionally narrower than arbitrary response recording.
  Non-empty bodies must be JSON, sensitive field names and signed URLs are
  rejected, and only bounded non-secret headers can be stored. Applications
  still own canonical request-digest construction, route opt-in policy, lease
  renewal for long requests, and mapping in-progress/conflict outcomes to HTTP.
- Idempotency state is an opt-in module with isolated Goose history. A
  transaction-bound store can complete the replay record with the business
  write, while default 5xx handling releases the claim instead of caching an
  infrastructure failure.
- API token authentication can remain isolated from browser sessions and the
  core schema. A pinned HS256 access contract carries issuer, audience,
  subject, expiry, family, and device identifiers; rotating opaque refresh
  tokens persist only a separate-key HMAC. A conditional active-to-used update
  plus child insert makes rotation portable and atomic across the three
  supported SQL dialects.
- Refresh replay evidence must outlive every token it can invalidate. The
  token module therefore retains used rows and revokes all active descendants
  when an old token is presented. Projects must not purge used rows before the
  maximum refresh lifetime, and must add a family-age policy if they do not
  want the supplied sliding refresh expiry.
- Stateless family logout cannot immediately invalidate an already-issued
  access token without an introspection/deny-list read. The core instead keeps
  access TTL short and always rechecks caller-owned user status; POSTA may add
  per-device introspection if its threat model requires immediate access-token
  revocation. HTTP transport, rate limiting, authorization, and audit remain
  adapter responsibilities.
- Audit actor identifiers cannot share the UUID-only constraint of user IDs:
  worker IDs and service identities are framework actors too. The public audit
  contract already permits 160 characters, so the database and GORM model must
  use that same bound or PostgreSQL rejects valid worker audit events.
- Resource metadata should be declarative and immutable, but it should not
  generate runtime CRUD through reflection. Requiring ownership, policy,
  operation references, DTO types, and explicit audit/sensitive field lists at
  registration gives generators a fail-closed input while services retain
  control over invariants and SQL-level scope.
- HTTP idempotency is optional but defaults to the portable database provider
  because the published write contract advertises `Idempotency-Key`. Projects
  may explicitly disable the module; a request that still sends the header then
  fails with a stable 503 problem instead of executing while silently ignoring
  the caller's safety expectation.
- The six built-in idempotent writes bind a key to the authenticated actor,
  method, registered route template, actual path, canonical query, content
  type, and exact request body. Their successful business mutation, audit row,
  and idempotency completion share one transaction. The initial execution claim
  necessarily precedes that transaction; a crash before the transaction
  commits is recovered by lease expiry and cannot leave a completed business
  write without its replay record.
- Future application-owned idempotent handlers must call the transaction-bound
  completion helper from their write transaction. The middleware has a
  fail-closed post-handler completion fallback for successful no-op paths, but
  using that fallback for a new business mutation would reintroduce a
  commit-to-completion crash window and must not be described as exactly once.
- Upload-intent replay deliberately stores only the public file projection and
  an empty upload descriptor. Every replay re-authorizes the pending file and
  asks the storage adapter for a fresh short-lived upload request, so signed
  URLs and signed headers never enter the idempotency table.
- Runtime composition still invoked bootstrap after its read-only schema
  check, so every API start could write permissions and administrator
  associations. The bootstrap entry point can derive the SQL dialect from
  GORM, reuse the built-in registry permissions, enforce the current schema
  with a read-only check, and run every seed mutation plus one CLI/system audit
  through the existing unit of work.
- Reading and revoking one's own browser sessions is an identity lifecycle
  capability rather than an application-role privilege. Injecting the two
  registered session permissions as intrinsic `own` grants after successful
  authentication keeps route middleware fail-closed while allowing every
  active user to access `/me`, current-device logout, and all-device logout.
- All-device logout does not need a caller-selectable resource identifier.
  Deriving the deletion scope exclusively from the authenticated principal's
  user ID prevents horizontal access, and sharing the audit unit-of-work
  transaction guarantees that an audit failure restores every deleted session.
- A direct-upload intent needs its own durable expiry delivery at the moment
  the metadata row is created. Scheduling `storage.cleanup` v2 for the signed
  request expiry plus a two-minute grace in the same file/audit transaction
  prevents committed pending rows from losing their recovery task.
- Cleanup delivery identity must include the lifecycle cause. A stale
  `pending-expiry` delivery is allowed to succeed as a no-op after confirmation,
  so explicit deletion and invalid-upload cleanup use different idempotency
  keys; otherwise the succeeded stale job could suppress a later required
  deletion.
- Pending expiry must lock and re-read metadata before touching storage, and
  confirmation must likewise require a locked `pending` row. This prevents a
  delayed expiry from deleting a ready object and prevents a confirmation
  racing after expiry from resurrecting deleted metadata.
- Object authorization metadata is insufficient unless runtime use can be
  verified. Holding successful responses until a registered handler consumes
  its declared all/actor/object/query mode prevents accidental unscoped output.
  It cannot undo arbitrary raw database side effects, so application modules
  must treat Gin handlers as trusted adapters and perform mutations through the
  injected audited unit of work.
- Compiled-in modules need runtime dependencies without importing Aginex
  `internal` packages. An immutable context snapshot of GORM, audited writes,
  object storage, optional transactional jobs, and observability keeps
  registration side-effect free and makes handlers, readiness, lifecycle, and
  worker deliveries testable without globals.
- W3C header validation and log correlation alone are not distributed
  tracing. The durable boundary must create a producer span, persist that exact
  child context atomically with the job, extract it in the worker, and create a
  consumer span before database or storage calls.
- Framework telemetry should define stable low-cardinality semantics while
  deployment code owns exporters. Keeping the recorder and sink provider
  neutral avoids global OTel state and mandatory infrastructure; POSTA still
  owns its business metrics, SLOs, dashboards, sampling, retention, and
  provider credentials.
- API readiness should verify only dependencies needed to serve API traffic;
  requiring a worker to be online would cause false failures during rolling
  deploys. Worker readiness is a separate bounded decision, while backlog,
  oldest-job age, stale leases, and dead jobs belong in metrics and alerts.
- A validated upload intent is not a size boundary unless the declared byte
  count participates in the cloud-provider signature. S3 accepts
  `PutObjectInput.ContentLength`; OSS V4 requires `Content-Length` as an
  explicitly configured additional signed header, so the OSS adapter owns that
  signer configuration instead of relying on callers to remember it.
- Built-in file deletion, invalid-upload cleanup, and pending-intent expiry are
  durable workflows. A production composition that registers `FilesModule`
  must reject `Jobs.Driver=disabled`; a zero-business composition does not
  acquire that dependency. Development and test configurations may still
  disable the worker for narrow local use.

## Final Remediation Decisions (2026-07-31)

- Framework composition is exact, not additive by accident:
  `application.Define()` has no business schema or route, while
  `FilesModule()` and `StarterExampleModule()` are explicit selections. The
  repository reference application selects both so the shipped web and E2E
  example remain functional.
- Every process consumes the same immutable definition and module fingerprint.
  API, Worker, migrate, bootstrap, and OpenAPI therefore cannot silently
  disagree about routes, permissions, migrations, or background handlers.
- Bundled module migrations have independent Goose histories, support empty
  installation and pre-stable adoption, and fail closed when only part of a
  legacy schema is present.
- Browser protocol names are framework constants. Runtime cookie/session/CSRF
  behavior and OpenAPI now share `aginex_session`, `aginex_csrf`, and
  `X-CSRF-Token`; production configuration cannot rename one side and cause
  contract drift.
- Shared rate limiting is a core provider contract because authentication and
  sensitive endpoints require cross-instance enforcement. Policy values and
  business-abuse detection remain application concerns.
- POSTA should implement its postal domain resources, custom ownership graph,
  SMS/Apple/WeChat identity adapters, quota values, abuse rules, business jobs,
  publication policy, and MFA user experience on top of these contracts.
- Source qualification is complete locally. Stable-release qualification is a
  separate evidence decision and still requires the live database, object
  storage, browser, container, SBOM, and security-scan gates configured in CI.

## Local Toolchain

- Go 1.25.3 on darwin/arm64.
- Node.js 22.19.0 and pnpm 10.33.2.
- Docker 29.4.0.
- Git 2.50.1; the workspace is an initialized repository with an intentionally
  uncommitted remediation worktree.
