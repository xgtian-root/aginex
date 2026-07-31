# Aginex production operations

This guide describes the deployable `api`, `worker`, `migrate`, and `web`
artifacts. The API and worker never modify schemas during startup. A release
must run migrations and the administrator bootstrap explicitly.

## Build immutable images

Build all images from the same source revision and inject the same release
metadata:

```bash
VERSION=0.1.0-rc.1
COMMIT="$(git rev-parse HEAD)"
BUILD_DATE="$(git show --no-patch --format=%cI HEAD)"

docker build --target api -t aginex/api:"$VERSION" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" .

docker build --target worker -t aginex/worker:"$VERSION" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" .

docker build --target migrate -t aginex/ops:"$VERSION" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" .

docker build --target web -t aginex/web:"$VERSION" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" \
  --build-arg NEXT_PUBLIC_API_URL=https://api.example.com .
```

`NEXT_PUBLIC_API_URL` is public configuration compiled into browser assets.
Changing it at container runtime does not rewrite an existing web build. Never
pass passwords, session secrets, database DSNs, or cloud credentials as build
arguments. Supply secrets to containers at runtime through the deployment
platform's secret store.

The distroless Go images use the numeric user `65532:65532` and contain no
shell; the web image uses `1000:1000`. No image receives development
credentials.

## Production configuration

Start from [`.env.example`](../.env.example), store the production copy outside
the source tree, and change every example credential. Production mode rejects
an insecure public URL, insecure allowed origins, an insecure session cookie,
an all-address trusted-proxy range, placeholder or whitespace-padded bootstrap
credentials, and session secrets that are short, templated, or low-diversity.
The session cookie, CSRF cookie, and CSRF header names are fixed by the
published OpenAPI contract.

A typical online deployment uses:

```dotenv
AGINEX_ENV=production
AGINEX_API_PUBLIC_URL=https://api.example.com
AGINEX_WEB_ORIGINS=https://admin.example.com
AGINEX_TRUSTED_PROXIES=10.20.0.0/24
AGINEX_SESSION_SECURE=true
AGINEX_SESSION_SAME_SITE=lax

AGINEX_DATABASE_DRIVER=postgres
AGINEX_DATABASE_DSN=postgres://aginex:REDACTED@postgres.example.com/aginex?sslmode=require
AGINEX_IDEMPOTENCY_DRIVER=database
AGINEX_JOBS_DRIVER=postgres
```

Only list proxy CIDRs that are controlled by the deployment. Each worker
replica needs a stable, unique `AGINEX_JOBS_WORKER_ID`; use the pod or task
identity rather than sharing the default value.

If the web application and API intentionally use different sites, review the
cookie `SameSite` mode and CSRF/origin requirements together. `SameSite=none`
still requires a secure cookie in production.

### Separate database roles

Do not inject one database credential into every process. The environment
variable can remain named `AGINEX_DATABASE_DSN`, but the deployment must give
it a different secret value according to the process:

| Process | Required database authority |
|---|---|
| `migrate` release job | Owns the application schema and migration objects; may take migration locks and execute the reviewed DDL/data migration |
| one-shot `bootstrap` job | May synchronize users, identities, permissions, roles, associations, and insert its audit event; remove the credential after the job |
| `api` | Runtime DML only for application tables; `audit_logs` is restricted to `SELECT` and `INSERT` |
| `worker` | Runtime DML only for queue and handler-owned tables; `audit_logs` is restricted to `SELECT` and `INSERT` |
| `web` | No database credential |

The API and worker roles must not own the database, application schema,
`audit_logs`, append-only triggers, or migration tables, and must not inherit
the migrator role. Revoke `UPDATE`, `DELETE`, and `TRUNCATE` on `audit_logs`,
and revoke schema/table DDL such as `CREATE`, `ALTER`, `DROP`, and trigger
management. A row-level append-only trigger is useful defense in depth, but a
table owner or DDL-capable runtime credential could remove or bypass it.

For PostgreSQL, keep `audit_logs` and its trigger owned by the migrator role,
revoke schema `CREATE` from runtime roles, then grant runtime roles only
`SELECT, INSERT` on that table. For MySQL, omit `UPDATE` and `DELETE` on
`audit_logs` and do not grant `ALTER`, `DROP`, `CREATE`, or `TRIGGER`; MySQL
`TRUNCATE` is controlled by DDL authority. Manage these grants in deployment
infrastructure, because concrete role names and database boundaries are
environment-owned. SQLite deployments must enforce the equivalent separation
with distinct file/process permissions; it is not a substitute for server-side
roles in a multi-instance online service.

## Release and rollback order

Use one configuration source for the operational command, API, and worker:

1. Back up the database and verify object-storage recovery procedures.
2. Inject the DDL-capable migrator DSN and run `aginex migrate status`.
3. Run `aginex migrate up` as a single release job, then remove that
   credential from the runtime deployment.
4. On first installation, and after built-in permission definitions change,
   run the repeat-safe `aginex bootstrap` with its one-shot DML credential,
   administrator email and password, then remove those values from normal
   runtime configuration.
5. Start or roll the API and worker with their restricted runtime DSNs.
6. Wait for API readiness, then roll the web application.

With the `migrate` image, the operational commands are:

```bash
docker run --rm --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --env-file /run/secrets/aginex.env \
  aginex/ops:0.1.0-rc.1 migrate up

docker run --rm --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --env-file /run/secrets/aginex-bootstrap.env \
  aginex/ops:0.1.0-rc.1 bootstrap
```

The migrate command uses versioned Goose migrations and a database lock. API
startup checks the core/rate-limit schema plus enabled idempotency and job
schemas. Worker startup checks the core/rate-limit and PostgreSQL job schemas.
Both fail instead of changing the database.

Prefer forward-compatible expand/migrate/contract releases. If an application
rollback is required, roll back to a binary compatible with the already
applied schema. Do not automatically run destructive `down` migrations in
production.

## Read-only containers

The cloud-storage/PostgreSQL deployment needs no writable application root.
Example API and worker controls:

```bash
docker run --detach --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --env-file /run/secrets/aginex.env \
  --publish 8080:8080 \
  aginex/api:0.1.0-rc.1

docker run --detach --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --env-file /run/secrets/aginex.env \
  aginex/worker:0.1.0-rc.1
```

For SQLite or local object storage, mount a dedicated writable volume at
`/data`; the Go images default the SQLite DSN and local storage root to paths
below that directory. A bind-mounted host directory must be writable by UID/GID
`65532`; do not make the whole root filesystem writable. Worker replicas using
local storage must mount the same object data.

The standalone web server can run read-only with writable ephemeral caches:

```bash
docker run --detach --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --tmpfs /app/apps/web/.next/cache:rw,nosuid,nodev,size=128m \
  --publish 3000:3000 \
  aginex/web:0.1.0-rc.1
```

## Health and shutdown

- `GET /health/live` reports only that the API process can serve HTTP.
- `GET /health/ready` verifies the database, every enabled migration set,
  configured object storage, and required module dependencies. Each check has
  its own timeout and the external failure response does not expose the
  dependency error.
- `/api/v1/health/live` and `/api/v1/health/ready` remain compatibility aliases.

Optional module readiness failures are logged but do not remove the process
from service. Required failures return `503`. S3 and OSS readiness use a
bucket-level probe, so production credentials must include the corresponding
least-privilege metadata permission. The worker exposes the same bounded
dependency decision through `Runtime.Ready(ctx)`; deployments may publish that
result on an internal listener or use a process probe without coupling API
readiness to worker availability.

Both API and worker handle `SIGTERM`. The API drains requests for
`AGINEX_HTTP_SHUTDOWN_GRACE_PERIOD`. The worker stops claiming new jobs,
cancels active handlers, and settles their leases as failed within the same
grace period so they can be retried. Configure the orchestrator's termination
grace period to exceed this value.

Logs are structured JSON on standard output. Preserve `request_id`,
`traceparent`, actor, route, status, and duration fields in the log pipeline;
secret-bearing fields are redacted by the runtime logger.

## Metrics and tracing

Aginex instruments HTTP requests, GORM operations and connection pools,
durable enqueue/claim/handler/heartbeat/settlement work, shared rate limiting,
and Local/S3/OSS storage operations. Incoming W3C `traceparent` values create a
new server child span. Transactional enqueue persists a producer context and
the worker restores it as a consumer span, so API, worker, database, and
storage activity can share one trace.

The framework emits only bounded dimensions such as route templates, status,
database system, job type/version, limiter namespace, storage provider, and
outcome. Raw paths, SQL and parameters, actor/request/job IDs, limiter keys,
payloads, object keys, bucket names, signed URLs, and provider error text are
not telemetry attributes.

The default composition uses a no-export recorder. A deployment that needs
metrics or traces must create a `framework/observability.Recorder` with a
concurrency-safe `Sink`, then attach it with
`application.Definition.WithObservability`. The deployment owns OTLP,
Prometheus, or another adapter; buffering, sampling, TLS and credentials;
collector availability; retention; and any internal `/metrics` listener.
Aginex deliberately does not expose a public metrics endpoint or install
process-global telemetry state.

## Durable jobs

`AGINEX_JOBS_DRIVER=postgres` is the production queue and requires
`AGINEX_DATABASE_DRIVER=postgres`. It provides at-least-once delivery:
handlers must remain idempotent. Claims use database row locking, leases are
heartbeated, failures retry with backoff and jitter, and exhausted jobs enter
`dead`.

When `application.FilesModule()` is part of the shared definition, the worker
registers both versions of `storage.cleanup`. A zero-business worker does not
register or depend on these handlers or the `file_objects` table. Version 2
distinguishes `explicit-delete` from `pending-expiry`. File deletion is a state
transition followed by a durable task. A direct-upload intent also atomically
schedules pending-object expiry for the signed request's expiry time plus a
two-minute grace period.

Production compositions that enable `FilesModule` must use
`AGINEX_JOBS_DRIVER=postgres` and keep at least one worker running. Development
compositions with jobs disabled do not have an automatic pending-upload expiry
task. Monitor overdue version-2 `storage.cleanup` jobs and file rows that
remain `pending` beyond the signed-upload window.

Monitor `aginex_jobs` by state, oldest `scheduled_at`, attempts, and
`heartbeat_at`. Alert on a growing pending backlog, stale running leases, or
dead jobs. System-scoped operators can use:

- `GET /api/v1/jobs` with `jobs:read`. It defaults to dead jobs and accepts
  `state`, exact `type`, `page`, and `pageSize` filters.
- `POST /api/v1/jobs/{uuid}/retry` with `jobs:retry`. It atomically changes only
  `dead` jobs back to `pending` and writes the audit event in the same
  transaction. An optional `Idempotency-Key` makes a successful retry safely
  replayable without a second state change or audit event.

The list deliberately omits payloads, payload hashes, idempotency keys,
`traceparent`, and raw error text. Use the protected API instead of editing
queue rows; retrying any non-dead state is rejected.

## Idempotency retention

The default `AGINEX_IDEMPOTENCY_DRIVER=database` scopes keys to the actor and
operation, stores request fingerprints and safe responses, and rejects reuse
with a different request. Lease duration must be shorter than or equal to the
record TTL. Disabling the driver causes requests that supply
`Idempotency-Key` to be rejected instead of silently ignoring the key.

Expired rows are no longer reusable after `AGINEX_IDEMPOTENCY_TTL`, but TTL
does not itself guarantee immediate physical deletion. High-volume
applications must schedule the framework store's `CleanupExpired` operation
and monitor table growth.

## Storage and recovery

Use random object keys and short-lived signed URLs. Keep buckets private unless
a file is explicitly public. For S3-compatible or OSS providers, use
least-privilege credentials limited to the configured bucket.

Database and object-storage backups form one recovery unit. Restore them to an
isolated environment, run `aginex migrate status`, and test both metadata reads
and object downloads. A database restore can legitimately contain pending
`storage.cleanup` jobs; start a worker only after the matching object-store
snapshot is available.

## CI and release evidence

CI enforces:

- module checksum verification, tests, race tests, vet, Skill validation, and
  atomic generated OpenAPI/client drift checks;
- PostgreSQL, MySQL, Local, and required S3-compatible storage contracts;
- full npm dependency audit, web typecheck/lint, tests, and standalone build;
- a Chromium workflow against real API, Next.js, and PostgreSQL processes that
  covers login/redirect protection, audited product CRUD, verified local image
  upload, durable cleanup enqueue and protected job inspection, and logout;
- Go vulnerability analysis, static security analysis, repository secret and
  configuration scanning;
- all four non-root image builds, CycloneDX SBOM generation, and image
  High/Critical vulnerability gates;
- read-only, capability-dropped runtime smoke checks. API and web readiness are
  probed, the migration image runs against SQLite, the worker runs against
  PostgreSQL, and long-running containers must exit cleanly after `SIGTERM`.

The integration-test packages share one PostgreSQL and one MySQL DSN, so CI
runs Go packages with `-p 1` to prevent independent migration tests from
dropping each other's module tables. OSS contract tests run only when the
repository's OSS test secrets are configured; set
`AGINEX_REQUIRE_OSS_CONTRACT=true` to make that provider mandatory for a
trusted push or manual release run. Pull-request jobs never receive OSS
credentials.

GitHub Actions and container bases are pinned to immutable commits or digests.
Review Dependabot updates before merging, especially security-scanner actions;
do not replace the reviewed Trivy action and version pins with floating tags.
