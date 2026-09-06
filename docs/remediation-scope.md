# Aginex production remediation scope

This document fixes the boundary between reusable Aginex capabilities and
application-owned POSTA behavior. It is intentionally narrower than a promise
that every deployment using Aginex is production-ready: a project must still
select providers, define policies, run the release gates, and operate its own
data.

## Decision rules

A capability belongs in the framework when omitting it would make every
multi-user application repeat a security boundary, consistency protocol, or
provider contract. A capability belongs in an application when its rules
depend on the domain, product policy, commercial plan, external identity
provider, or operational risk appetite.

Optional infrastructure is still a framework capability. It remains opt-in
when forcing it would violate Aginex's SQLite, PostgreSQL, and MySQL portability
or would add an unnecessary service dependency to every application.

## Framework core

| Capability | Framework responsibility | Current baseline |
|---|---|---|
| Module composition | Deterministic registration, duplicate detection, operations, permissions, resource metadata, migrations, jobs, readiness and lifecycle hooks, runtime handlers, and fail-closed validation | Implemented end to end; API handlers and lifecycle hooks receive provider-neutral runtime services through context |
| Browser identity | Separate user and provider identity records, administrator-managed user CRUD, activation and password reset, Argon2id password provider, revocable server-side sessions, secure cookies, CSRF, safe redirects, one-device and all-device logout | Implemented; public self-registration remains disabled |
| Authorization | `resource:action` grants plus `own`/`all` scope, user-role assignment, role CRUD and grant replacement, system actors without an implicit bypass, object checks, and SQL-level list scopes | Access-management implementation is present; complete authorization and live-dialect release gates remain listed below |
| API contract | `/api/v1`, explicit DTOs, typed success and problem responses, security schemes, deterministic OpenAPI, and a generated web client | Implemented for built-in and compiled-in application module operations, including runtime request/response validation |
| Transaction safety | Business mutation, successful audit event, and idempotency completion share one transaction | Implemented for built-in writes; the same public unit-of-work boundary is injected into application module contexts |
| Audit | Bounded actor/request context, redacted before/after values, append-only database enforcement, and rollback on audit failure | Implemented |
| HTTP safety | Request/header/body limits, trusted proxy configuration, explicit credentialed CORS origins, shared rate limits, secret redaction, production fail-fast, graceful shutdown | Implemented |
| Shared rate limiting | Cross-instance login, upload, and sensitive-operation limits with portable database windows, privacy-preserving keys, fail-closed storage errors, retry metadata, and cleanup | Implemented as a core security boundary without requiring Redis |
| Migration lifecycle | Goose as schema authority, API-owned automatic migrations under a durable high-authority DSN, database locking, permission-drift synchronization, empty-install and upgrade tests | Implemented; the worker remains non-mutating and live PostgreSQL/MySQL execution remains a release environment gate |
| Storage primitives | Provider-neutral object operations, random object-key helpers, typed short-lived signed requests, bounded content verification, and Local/S3/OSS contracts | Implemented without imposing a file-object business table or HTTP API; live provider execution remains a release-environment gate |
| Observability contract | W3C context propagation, low-cardinality spans and metrics for HTTP, database, jobs, rate limiting and storage, connection-pool gauges, and bounded readiness checks | Implemented as a vendor-neutral recorder/sink contract; exporter selection and operation belong to each deployment |
| Delivery contract | Independent API, worker, and web artifacts; one-time browser Setup; non-root/read-only-compatible images; health endpoints; release metadata | Implemented in source and CI; local Docker daemon verification may still be unavailable |

### Built-in access administration

User and role administration belongs to the framework security boundary rather
than the starter example. Authorized operators can create, read, update, delete,
enable, and disable users, reset local passwords, assign roles, manage roles, and
replace permission grants. Public self-registration is intentionally excluded.

Permission definitions are owned by compiled module code and synchronized
idempotently at startup. The administration surface assigns those registered
definitions; it does not turn permissions into user-authored database content.
The system-managed `Administrator` role receives every registered permission at
`all` scope and cannot be renamed, deleted, or weakened.

Access mutations preserve at least one usable administrator, reject operations
that would lock out the acting administrator, and enforce a delegation ceiling:
an operator cannot grant a permission or scope they do not hold. These invariants
must remain transactionally coupled to session revocation and audit recording.
Their full 401/403/success, concurrency, and live-dialect matrices remain part of
release qualification rather than being inferred from UI availability.

## Official opt-in framework modules

| Module | Why it is optional | Framework responsibility |
|---|---|---|
| File objects (`application.FilesModule`) | Some applications use external media services or have no user uploads; a storage provider alone must not create a file business model | Owner-scoped metadata and HTTP operations, private-by-default arbitrary-type direct/resumable transfer, streamed verification, safe preview/download, multipart/orphan/deletion cleanup, and one isolated current Goose baseline for each supported dialect |
| Starter example (`application.StarterExampleModule`) | Products and a product-backed dashboard demonstrate a vertical slice but are not reusable framework concepts | Example-only product CRUD, dashboard summary, permissions, audit behavior, typed contracts, and an isolated three-dialect Goose schema |
| PostgreSQL jobs | A durable PostgreSQL queue cannot be a mandatory dependency of SQLite or MySQL applications | Versioned payloads, transactional enqueue, leases, heartbeat, at-least-once delivery, retry/backoff/dead state, actor/trace propagation, and protected dead-letter operations |
| API token authentication | Not every administration application exposes a public client API | Short-lived access tokens, opaque rotating refresh tokens, keyed hashes at rest, replay-family revocation, device/family/user revocation, provider/subject-to-local-user mapping, active-subject lookup, and strict Bearer middleware; concrete login/refresh/revoke HTTP operations remain application modules so their audit and rate-limit policy is explicit |
| Idempotency | Some applications or internal-only routes do not need replay semantics | Actor/operation/request binding, conflict detection, safe bounded response replay, leases, expiry, and transaction-bound completion |

Opt-in does not mean "best effort." When an operation advertises one of these
contracts, disabling its provider must fail explicitly rather than silently
weakening behavior.

An empty `application.Define()` is the zero-business composition. It creates no
`products` or `file_objects` table and publishes no products, dashboard, or
files API contract. The checked-in starter distribution explicitly registers
both bundled modules so its existing web pages remain aligned with its API.
Production compositions that register `FilesModule` must configure the durable
PostgreSQL jobs provider; compositions that omit it are not subject to that
file-cleanup gate.

## Application-owned behavior

POSTA, rather than Aginex core, owns:

- postmark, post office, calibration, collection, publication, and other
  product-domain models and workflows;
- the exact ownership graph and custom authorization policies for those
  resources;
- SMS, Apple, WeChat, or other identity-provider adapters and their credential
  exchange endpoints;
- concrete storage quota plans, billing rules, abuse thresholds, retention
  periods, and public-file publication rules;
- business job types, payload contents, notification templates, and the
  idempotency behavior of each handler;
- moderation, approval, publishing, search-ranking, and data-quality rules;
- the choice and operation of antivirus, image transformation, telemetry
  exporters/servers, email/SMS, and backup vendors;
- business metrics, SLOs, dashboards, alert thresholds, sampling, and retention;
- user-facing MFA enrollment, account recovery, and high-risk confirmation
  experiences.

The framework may expose interfaces and safe lifecycle primitives for these
features, but it must not embed POSTA concepts or choose product policy.

## Deferred framework tooling

The following are useful framework work, but are not prerequisites for safely
starting POSTA on the current compiled-in modular monolith:

- module/resource generators beyond the implemented project initializer;
- automatic thumbnails, EXIF cleanup, and antivirus adapters;
- a formal API deprecation calendar and compatibility-report UI;
- multi-tenancy, hot-loaded plugins, a microservice split, or mandatory Redis.

Generated scaffolding must eventually emit permissions, DTOs, audit coverage,
migrations, OpenAPI metadata, and tests by default. Until then, the checked-in
Agent Skills are the supported development workflow.

## Release qualification

Source implementation is only one part of the production gate. A release
candidate must also retain evidence for:

1. the complete Go test, race, vet, OpenAPI/client drift, web test, and
   production-build gates;
2. empty installation and previous-version upgrade on every claimed database;
3. live Local, S3-compatible, and OSS storage contract runs for every provider
   claimed by the release;
4. authentication, two-user authorization, audit rollback, file lifecycle,
   idempotency, worker crash/retry, and generated-client end-to-end scenarios;
5. image builds, non-root execution, SBOM generation, secret scanning, and
   vulnerability gates;
6. a tested database/object-storage backup and restore procedure.

Unavailable external services are reported as unexecuted gates, never as
passing checks. Aginex remains a pre-release foundation until all gates required
by the target deployment have evidence.
