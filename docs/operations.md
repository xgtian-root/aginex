# Aginex production operations

Aginex ships three deployable artifacts: `api`, `worker`, and `web`. Database
migrations and built-in access synchronization are part of API initialization;
there is no migration image or operational migrate/bootstrap CLI.

## Deployment contract

The current release has four important operating constraints:

1. Run exactly one API instance. The API applies pending Goose migrations and
   synchronizes built-in permissions and roles before the application becomes
   ready. Concurrent API startup and rolling multi-replica deployment are not
   supported.
2. Use PostgreSQL for production. SQLite and MySQL remain supported for local
   development, tests, and compatibility verification, but are not production
   deployment targets.
3. Give the API database DSN enough authority to own and migrate the
   application schema and to synchronize bootstrap data. A restricted
   DML-only API role no longer works because initialization is server-owned.
4. Start or restart workers only after the API reports application mode and
   passes readiness. Workers validate their dependencies but never initialize
   an unconfigured installation or apply migrations.

The privileged API DSN increases the consequence of an API compromise. Keep it
in the deployment platform's secret store, restrict network access to the
database, scope it to one Aginex database, audit DDL, and do not expose it in
build arguments, logs, crash reports, or web configuration.

## Build immutable images

Build every image from the same source revision and inject the same release
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

docker build --target web -t aginex/web:"$VERSION" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" .
```

The web build leaves `NEXT_PUBLIC_API_URL` empty by default. Browser requests
therefore use same-origin API URLs. At the production ingress, route `/api/*`
and `/health/*` to the Go API and every other path to the Next container. For
example, the essential Nginx-style split is:

```nginx
location /api/ {
    proxy_pass http://aginex-api:8080;
}
location /health/ {
    proxy_pass http://aginex-api:8080;
}
location / {
    proxy_pass http://aginex-web:3000;
}
```

The Next server also probes API mode during server rendering. Inject
`AGINEX_API_INTERNAL_URL=http://api:8080` (using the deployment's real internal
service name) into the web container at runtime. This server-only variable is
not exposed to the browser and is not a build argument. Its fallback,
`http://127.0.0.1:8080`, is valid only when API and web share a host, such as
local development; it cannot reach a separate API container.

If a deployment intentionally uses a separate public API origin, pass its
absolute origin as
`--build-arg NEXT_PUBLIC_API_URL=https://api.example.com`. This value is public
and compiled into browser assets; changing it at container runtime cannot
rewrite an existing build.

Never pass passwords, session secrets, database DSNs, or cloud credentials as
build arguments. The distroless Go images run as `65532:65532` and contain no
shell; the web image runs as `1000:1000`.

## Installation state and `/data`

The API image sets:

```dotenv
AGINEX_CONFIG_FILE=/data/aginex-config.json
AGINEX_STORAGE_LOCAL_ROOT=/data/uploads
```

It deliberately does not set a database DSN. Mount a durable volume at
`/data` even when object data lives in S3 or OSS. Browser Setup persists a
versioned installation document there with mode `0600`; it includes the
backend-assembled managed database DSN, session secret, and console-managed
storage profiles (including static cloud access keys) plus the file-upload
policy. Treat the file as a
secret, back it up, and make the volume writable only by UID/GID `65532`.

An environment-configured installation keeps its DSN environment-owned while
persisting the database driver, session secret, storage/profile state, and file
policy in the file. Before an explicit release boundary, readers and writers use
one strict current installation document whose version is always `1`, including
when unpublished fields change. A configured file remains fail-closed for
invalid JSON, unknown fields, unsafe permissions, a symlink, any other version,
or a mismatch with database environment variables.
Do not delete or replace a committed file merely to re-run Setup. Recover it
from backup or repair the deployment configuration deliberately.

### Reinitialize stale local pre-release state

Stop the local API and worker, then inspect the exact target without changing
anything:

```bash
go run ./cmd/aginex dev reinitialize
```

The dry run prints a sanitized database target, a private backup directory, and
the exact `--confirm` value. Re-run that printed command to execute. The command
is disabled in production, refuses environment-managed and non-loopback server
databases, requires a regular mode-0600 installation file, and never prints the
DSN or credentials. It archives the installation marker and preserves database
state before returning the application to browser Setup: SQLite moves the
checkpointed database into the private backup directory, PostgreSQL creates a
private timestamped backup schema and transactionally moves application-owned
tables, views, sequences, and functions into it without changing the shared
`public` schema owner or grants, and MySQL moves base tables to a timestamped
backup database. PostgreSQL fails
closed before mutation when the application role lacks database `CREATE`
privilege or `public` contains foreign-owned/unsupported standalone objects.
Object-storage bytes are deliberately retained and may become unreferenced. The
private backup directory contains the original installation
marker and a credential-free `manifest.json`; SQLite also stores the database
file there, while the manifest identifies the PostgreSQL schema or MySQL
database retained on the local server. There is intentionally no automatic
restore command: stop services and have the database operator reverse the
recorded archive before restoring the marker. Complete Setup with a new
administrator after the reset.

## First-run browser Setup

Start the API with an empty `/data` volume and leave both
`AGINEX_DATABASE_DRIVER` and `AGINEX_DATABASE_DSN` unset. The stable
`GET /api/v1/system/mode` endpoint then returns `{"mode":"setup"}`, and the web
application redirects to the Setup page.

Setup has no setup token and no authenticated administrator exists yet. Its
write endpoints still enforce the configured browser-origin allowlist, CSRF,
request limits, and rate limiting, but those controls do not establish operator
identity. Until Setup completes, expose the API and web application only on a
trusted provisioning network or through an operator-controlled tunnel. Do not
put an unconfigured instance on a public ingress.

Run only one API container during Setup. Enter the production PostgreSQL host,
port, database name, schema-owner username and password, and SSL mode, plus the
initial administrator email and a strong password. Browser Setup does not
accept a raw PostgreSQL connection string; the backend validates these fields
and assembles the managed DSN. The completion flow:

1. assembles the DSN, then validates and pings the database;
2. applies pending core and enabled-module migrations;
3. synchronizes built-in access and creates or verifies an active administrator;
4. starts the application and verifies readiness;
5. atomically publishes `AGINEX_CONFIG_FILE`; and
6. switches the same HTTP server from Setup routes to application routes.

The status endpoint exposes only bounded stages and safe error codes; it never
returns submitted credentials, the assembled DSN, or provider error text. A
failure before the configuration commit leaves Setup active and does not
publish a partial file. Database work may already have completed, so correct
the reported dependency or permission problem and retry the repeat-safe
initialization. If a reviewed migration is incompatible, restore the database
backup rather than attempting an automatic down migration.

After activation, `/api/v1/setup/*` returns `404`; it cannot be used to replace
the database configuration. Rotate a stored DSN by an offline, backed-up
configuration operation and validate the replacement before restoring service.

## Environment-configured first start

Automation can bypass browser Setup by setting both database variables before
the first API start:

```dotenv
AGINEX_DATABASE_DRIVER=postgres
AGINEX_DATABASE_DSN=postgres://aginex_owner:REDACTED@postgres.example.com/aginex?sslmode=require
AGINEX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
AGINEX_BOOTSTRAP_ADMIN_PASSWORD=use-a-secret-generated-value
```

The API migrates and bootstraps the database, verifies readiness, then seals an
environment-backed installation marker. Remove the two bootstrap variables
after an active administrator exists. Keep the database variables present on
future starts; the marker records no DSN and startup fails if either value is
missing or the driver changes.

## Production configuration

Start from [`.env.example`](../.env.example), keep the deployed copy outside the
source tree, and supply secrets through the platform's secret mechanism. A
typical same-origin PostgreSQL deployment includes:

```dotenv
AGINEX_ENV=production
AGINEX_CONFIG_FILE=/data/aginex-config.json
AGINEX_API_PUBLIC_URL=https://admin.example.com
AGINEX_WEB_ORIGINS=https://admin.example.com
AGINEX_TRUSTED_PROXIES=10.20.0.0/24
AGINEX_SESSION_SECURE=true
AGINEX_SESSION_SAME_SITE=lax

AGINEX_IDEMPOTENCY_DRIVER=database
AGINEX_JOBS_DRIVER=postgres

AGINEX_STORAGE_DRIVER=s3
AGINEX_STORAGE_BUCKET=aginex-production
AGINEX_STORAGE_REGION=us-east-1
AGINEX_STORAGE_ENDPOINT=https://objects.example.com
```

For browser Setup, omit database and bootstrap variables. For preconfigured
startup, add the PostgreSQL and one-time administrator values described above.
Production validation also rejects HTTP public URLs, non-HTTPS browser origins,
insecure cookies, all-address trusted-proxy ranges, and weak session or
bootstrap secrets.

Only list proxy CIDRs controlled by the deployment. Each worker replica needs a
stable, unique `AGINEX_JOBS_WORKER_ID`; use its pod or task identity. S3/OSS
credentials should be limited to the configured bucket and the operations
needed by readiness, signed transfers, verification, and deletion.

## Startup and release order

For every release:

1. Back up the PostgreSQL database, `AGINEX_CONFIG_FILE`, and object storage as
   one recovery unit.
2. Stop the worker so it cannot consume jobs against a partially upgraded
   application.
3. Stop the old API and start exactly one new API instance with the privileged
   DSN. This is a recreate deployment, not a rolling multi-replica rollout.
4. Wait for `GET /api/v1/system/mode` to return `application`, then require
   `GET /health/ready` to return `200`.
5. Start workers and verify that they stay running and can reach PostgreSQL and
   object storage.
6. Deploy the web image and route same-origin API traffic at the ingress.

The Setup-mode readiness endpoint reports the provisioning server itself as
ready, so `/health/ready` alone is not sufficient to release workers. Always
gate them on application mode as well.

API initialization is bounded and fail-closed. If migration, bootstrap,
database, storage, or module readiness fails, the configured API exits before
listening. Inspect its redacted logs, correct the dependency, and retry one
instance. Aginex has not published a stable compatibility contract: an older
binary is not guaranteed to read the current strict v1 document or Files schema.
Prefer a forward fix; if rollback is unavoidable, restore the matching database,
installation file, and object-store backup as one unit. Never automate
destructive down migrations during rollback.

## Read-only containers

The images declare `/data` as persistent, but production should still name the
volume explicitly. Example API controls:

```bash
docker volume create aginex-data
docker network create aginex

docker run --detach --name aginex-api \
  --network aginex \
  --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --mount type=volume,source=aginex-data,target=/data \
  --env-file /run/secrets/aginex.env \
  --publish 127.0.0.1:8080:8080 \
  aginex/api:0.1.0-rc.1
```

After the API reaches application mode and readiness, start the worker with the
same installation configuration. A Setup-managed installation therefore needs
the same `/data` volume (or a secure read-only projection of its configuration
file). Environment-configured workers may receive their own PostgreSQL DSN,
but must use the same driver and database. The worker actively waits for both
application mode and readiness through `AGINEX_API_PUBLIC_URL`, so that URL
must also be reachable from the worker network:

```bash
docker run --detach --name aginex-worker \
  --network aginex \
  --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --mount type=volume,source=aginex-data,target=/data \
  --env-file /run/secrets/aginex-worker.env \
  aginex/worker:0.1.0-rc.1
```

Local object storage also requires the API and worker to share `/data/uploads`.
For a bind mount, pre-create the directory for UID/GID `65532`; do not make the
whole root filesystem writable.

The standalone web server can run read-only with an ephemeral cache:

```bash
docker run --detach --name aginex-web \
  --network aginex \
  --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges=true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --tmpfs /app/apps/web/.next/cache:rw,nosuid,nodev,size=128m \
  --env=AGINEX_API_INTERNAL_URL=http://aginex-api:8080 \
  --publish 127.0.0.1:3000:3000 \
  aginex/web:0.1.0-rc.1
```

## Health, shutdown, and observability

- `GET /api/v1/system/mode` distinguishes `setup` from `application` for the
  lifetime of the API process.
- `GET /health/live` reports that the current Setup or application HTTP surface
  can serve requests.
- In application mode, `GET /health/ready` verifies the database, enabled
  migration sets, configured storage, and required module dependencies.
- `/api/v1/health/live` and `/api/v1/health/ready` are compatibility aliases.

Required readiness failures return `503`. S3 and OSS readiness use a
bucket-level probe, so credentials must include the corresponding metadata
permission. Workers do not expose a public HTTP listener; use process state and
redacted startup logs as their deployment probe.

API and worker handle `SIGTERM`. The API drains requests for
`AGINEX_HTTP_SHUTDOWN_GRACE_PERIOD`. The worker stops claiming jobs, cancels
active handlers, and settles their leases so they can be retried. Set the
orchestrator termination grace period above the application value.

Logs are structured JSON on standard output. Preserve request and trace IDs,
actor, route, status, and duration fields; secret-bearing fields are redacted.
The default composition records bounded telemetry internally but configures no
export sink. A deployment owns its metrics/tracing adapter, buffering,
sampling, TLS, credentials, collector availability, and private metrics
listener.

## Durable jobs and idempotency

`AGINEX_JOBS_DRIVER=postgres` is the production queue. It provides at-least-once
delivery, so handlers must be idempotent. Claims use database row locking,
heartbeated leases, retry backoff and jitter, and a terminal `dead` state.
Production compositions with `FilesModule` must keep at least one worker
running after the API becomes ready.

Resumable sessions enqueue the versioned `storage.multipart.cleanup` job for
expiry and cancellation. The API also runs a safety-net scan every 15 minutes,
bounded to 100 sessions per pass, so SQLite/MySQL development and an interrupted
job enqueue cannot leave abandoned sessions unbounded. Monitor cancelling and
expiring session age as well as the general queue; repeated provider abort
failures remain retryable and require operator investigation.

Monitor queue state, oldest `scheduled_at`, attempts, and `heartbeat_at`.
System-scoped operators can use `GET /api/v1/jobs` with `jobs:read` and
`POST /api/v1/jobs/{uuid}/retry` with `jobs:retry`; use these protected APIs
instead of editing queue rows.

Database idempotency scopes keys to the actor and operation, stores request
fingerprints and safe responses, and rejects reuse with different input. TTL
makes expired records non-replayable but does not immediately delete them;
high-volume deployments must schedule cleanup and monitor table growth.

## Storage and recovery

Use random object keys and short-lived signed URLs. Keep buckets private unless
an object is explicitly public. Database, installation configuration, and
object-storage backups form one recovery unit. Restore all three to an isolated
environment, start one API against the restore, require application mode and
readiness, then test metadata reads and object downloads. Start a worker only
after the matching object snapshot is available because a restored database
may contain pending cleanup jobs.

The Object Storage settings page supports Local, Alibaba OSS, AWS S3, MinIO,
and Cloudflare R2 profiles. Changing the default affects only new uploads after
the API and worker restart; historical files keep their original profile and
objects are never copied automatically. Profiles referenced by file metadata
or unfinished cleanup work cannot be deleted. In production, custom MinIO
endpoints must use HTTPS and their hosts must be listed in
`AGINEX_STORAGE_ENDPOINT_ALLOWLIST`; userinfo, query strings, fragments,
link-local addresses, and cloud metadata endpoints are always rejected.

## File upload policy and provider requirements

The installation-wide file policy is independent from the active storage
profile. Its default maximum is 10 MiB; administrators can choose 1 MiB through
1 GiB in whole-MiB increments and can enable resumable uploads, which are off by
default. Policy editing remains available when the storage provider itself is
environment-managed. The settings API exposes pending and runtime values under
the same revision/ETag boundary as storage profiles. Saving either storage or
file-policy changes requires restarting both API and worker; the old processes
continue using their immutable runtime snapshot until then.

Each accepted upload records its creation-time policy and storage profile.
Later disabling resumable uploads, lowering the maximum, or switching the
default profile does not interrupt that upload. New requests use the restarted
runtime policy. The upload page obtains the effective limit and provider
capability from `GET /api/v1/files/upload-policy`; do not infer them from a
checked-in frontend constant.

The transfer contract is fixed:

- A selection contains at most 20 non-empty files. There is no extension or
  content-type whitelist, but the API rejects invalid MIME syntax, size
  mismatches, and files over the runtime maximum.
- Files at or below 32 MiB use a single upload. With resumable uploads enabled
  and a multipart-capable provider, files strictly larger than 32 MiB use fixed
  32 MiB parts, no more than 32 parts, and a 24-hour session. Part signatures
  expire after 10 minutes. Single-upload credentials expire after 10 minutes
  for files at or below 32 MiB and after 60 minutes for larger files.
- Refresh recovery is same-device assistance, not durable browser storage. The
  user must reselect the original file and pass the SHA-256 fingerprint over
  name, size, last-modified time, and the first/middle/final 1 MiB samples.
  `File`/`Blob`, signed URLs, provider upload IDs, ETags, and credentials must
  not be stored in browser persistence.

For S3, MinIO/R2, and OSS browser transfers, configure bucket CORS for every
deployed web origin. It must allow `PUT` and all headers present on the signed
request, including `Content-Type`, `Content-Disposition`, and
`Content-Length`. S3-compatible requests also bind `If-None-Match`; OSS binds
`x-oss-forbid-overwrite`. These provider-specific headers give a single-object
PUT create-only semantics so a stale signed request cannot overwrite a ready object. CORS must
also expose the `ETag` response header. The browser reads that opaque value
immediately after each multipart PUT so the API can acknowledge the part;
without `Expose-Headers: ETag`, multipart upload cannot complete.
Provider references: [AWS S3 multipart overview](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html),
[Cloudflare R2 uploads](https://developers.cloudflare.com/r2/objects/upload-objects/),
[Alibaba OSS CompleteMultipartUpload](https://www.alibabacloud.com/help/en/oss/developer-reference/completemultipartupload),
and [OSS ETag CORS guidance](https://help.aliyun.com/en/oss/the-please-set-the-etag-of-expose-headers-in-oss-error-message-is-returned-when-you-use-multipart-upload-to-upload-files).
Server readiness proves bucket access but does not prove browser CORS. Before a
release, perform a signed PUT from the real web origin, confirm the browser can
read `ETag`, then cancel the test session. Do not copy its signed URL or ETag
into deployment logs or tickets.

Configure the cloud bucket's incomplete-multipart lifecycle as a final safety
net, aborting incomplete uploads no earlier than 48 hours after initiation. The
application's 24-hour session cleanup remains the primary mechanism; the bucket
rule covers database loss, prolonged API/worker outage, and provider operations
that never reached acknowledgement. Ensure the provider credentials can create,
list, complete, and abort multipart uploads in addition to the existing object
read/write/delete and readiness operations.

Local direct and part uploads stream to staging files under the configured
storage root, enforce the exact byte count, fsync, and publish by atomic rename.
Provision capacity for both the staging data and final object during completion;
API and worker must share the same root. The API's binary upload routes can run
for up to 60 minutes, while the global JSON request limit remains unchanged.
The same bounded API scanner examines at most 100 canonical Local multipart
staging directories per pass and removes an unowned directory only after it is
older than 48 hours. It never follows symlinks or deletes staging that still has
a database session; monitor the storage root if the scanner repeatedly reports
filesystem errors.

All providers store opaque object bytes as `application/octet-stream`; the API
streams the completed object to calculate SHA-256, verify the exact size, detect
its real MIME, and validate preview structure. Only valid JPEG, PNG, WebP, GIF,
and PDF are eligible for inline preview. SVG, HTML, text, Office files, archives,
executables, malformed preview candidates, and every other type are returned as
`application/octet-stream` attachments. Local reads add
`X-Content-Type-Options: nosniff`; PDF UI preview uses a sandboxed iframe, and
download filenames use safe `filename*` encoding. File URLs accept
`purpose=preview|download`: an omitted purpose behaves as a preview only for a
verified safe image/PDF, while `download` always requests attachment behavior.

The current baseline does not provide antivirus scanning, file versioning,
folder upload, or automatic cross-device resume. Deployments that require
malware policy must add a quarantine/scanning workflow before exposing uploaded
files; changing the filename or browser MIME is not a substitute.

Treat `FilesModule` Down as application data retirement, not a routine binary
rollback. First stop new file writes, disable resumable uploads, restart API and
worker, and drain every non-terminal session by completing it or cancelling /
expiring it and confirming the provider multipart upload was aborted. The
migration guard rejects Down while any non-terminal session remains. It then
drops `file_upload_parts`, `file_upload_sessions`, and `file_objects` in that
order. All file metadata is lost and a later Up cannot reconstruct it; object
bytes may remain unreferenced. Proceed only with a restore-tested database,
installation-file, and object-store backup.

## CI and release evidence

CI enforces backend tests and static checks, generated contract drift, web
typecheck/lint/tests/build, PostgreSQL/MySQL/storage contracts, browser
workflows, vulnerability and secret scanning, and SBOM/vulnerability gates for
all three images. The serial browser workflow starts with a unique, absent
installation file, completes real browser Setup against PostgreSQL, and then
runs authenticated application workflows. Runtime smoke tests run non-root,
read-only, capability-dropped containers: the API initializes SQLite itself,
the worker is started only after a helper API initializes PostgreSQL and
reports application mode and readiness, and the web image is built with
same-origin API configuration. Long-running containers must exit cleanly after
`SIGTERM`.

GitHub Actions and container bases are pinned to immutable commits or digests.
Review dependency and scanner updates before merging; do not replace reviewed
pins with floating tags.
