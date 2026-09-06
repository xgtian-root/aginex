# Pre-stable remediation upgrade

This is a deliberately breaking upgrade from the 2026-07-30 `main` baseline.
Aginex has not published a stable API, so insecure prototype behavior is
removed instead of being preserved as a compatibility mode.

## Required application changes

1. Define the complete compiled-in module set once with
   `application.Define`. Reuse that `Definition` for API, worker, automatic
   initialization, and OpenAPI entry points. Compare its fingerprint across
   deployed processes. An empty definition no longer includes the sample
   products, dashboard, or file-object API. Existing starter deployments must
   opt in:

   ```go
   application.Define(
       application.FilesModule(),
       application.StarterExampleModule(),
   )
   ```
2. Give the API's persisted database account both runtime and DDL authority.
   Start exactly one API first: it applies the selected Goose migrations,
   synchronizes access drift, and must report application mode plus readiness
   before workers start. Workers remain schema-read-only. No runtime uses
   `AutoMigrate`, and there is no separate migrate/bootstrap CLI or image.
3. Update every protected `module.OperationDefinition` to declare an
   `Authentication` scheme, lowercase permission, and policy reference. Public
   operations must be explicitly `Public`.
4. Update every protected `module.HTTPRoute` to use `AuthorizedHandler` and an
   `Authorization` mode:
   - `all` for an admitted global grant;
   - `actor` only when the target comes exclusively from the authenticated
     actor;
   - `object` when the handler calls `RequestAuthorization.Check`;
   - `query` when the handler uses the query returned by
     `RequestAuthorization.Scope`.
5. Update custom `authz.Policy` implementations for the explicit
   `Admit`, `Check`, `Scope`, and `AllowsSystem` contract. There is no implicit
   system or administrator bypass.
6. Register an `APIContract` for every external operation. Database models
   cannot be request or response DTOs. Regenerate OpenAPI and the web client,
   then remove handwritten endpoint types.
7. Resolve `services.Runtime` from handler, readiness, lifecycle, and job
   contexts. Run every business mutation through `Runtime.Writes.Run`, return a
   valid sanitized audit event, and use the transaction-bound job queue when
   enqueue must commit with the write.
8. Use the public `server/framework/storage.ObjectStore` contract. Object keys remain
   private metadata; signed URLs must not be logged, audited, or placed in an
   idempotency replay record. Resolve multipart and controlled-read behavior as
   optional provider capabilities instead of importing cloud SDK types.
9. If the deployment exports telemetry, create one
   `observability.Recorder`, attach it with `Definition.WithObservability`, and
   implement a concurrency-safe deployment sink. Do not install global
   telemetry providers from a reusable module.
10. Keep the unpublished installation document at strict version `1`, including
    `fileUploadPolicy: {maxUploadBytes,resumableUploadsEnabled}`. Do not bump the
    version or add old-version readers until a release boundary and its upgrade
    contract are explicitly declared.

## Current-only configuration and schema baseline

Aginex remains pre-release and intentionally carries no promise that an older
binary, installation draft, generated client, or module-migration family can be
mixed with this baseline. Until publication, the installation loader and writer
use one strict current document whose `version` is always `1`; unpublished field
changes replace that baseline without increasing the number. Unknown fields and
every other version fail closed. Use `aginex dev reinitialize` to archive stale
local state rather than adding normalization or compatibility branches.

The core's reserved historical product/file steps remain no-ops; business
tables belong to opt-in modules. `FilesModule` has exactly one current migration
family with one `00001_files.sql` baseline for each of SQLite, PostgreSQL, and
MySQL. That baseline creates `file_objects`, `file_upload_sessions`, and
`file_upload_parts`; there are no `files_current`, `files_legacy`, adoption, or
upgrade families. `StarterExampleModule` likewise owns its isolated product
history. Do not introduce compatibility migrations for unpublished schemas
unless a user explicitly requests and defines that migration scope.

The files baseline Down is intentionally destructive and guarded. Before
running it, stop new file writes, disable resumable uploads, restart API and
worker, then complete or cancel/expire every non-terminal session and verify
that its provider multipart upload has been completed or aborted. Down refuses
to proceed while a non-terminal session exists; once clear, it drops
`file_upload_parts`, then `file_upload_sessions`, then `file_objects`. This
permanently removes file metadata and is not reversible by running Up again.
Take and restore-test a database/configuration/object-store backup first.

Roll out one API instance before starting workers. Its automatic migration,
runtime startup, and readiness include only the migration bundles in the
selected definition. A zero-business definition therefore does not require
either bundled history; the checked-in starter definition requires both.

In production, registering `FilesModule` also requires
`AGINEX_JOBS_DRIVER=postgres` so file deletion and orphan recovery remain
durable. A definition without the files module may keep jobs disabled.
For local development, `aginex dev` automatically supervises the independent
worker whenever that PostgreSQL jobs driver is configured.

Saving a storage profile or file-upload policy changes only the pending
installation revision. Restart both API and worker to load it. Existing upload
sessions retain their creation-time provider, size, and multipart parameters;
do not cancel them merely to apply a new default.
Alibaba OSS profiles must also provide the browser-visible HTTPS
`accessBaseUrl`. After upgrading an existing OSS profile, run
`aginex dev reconcile-storage-presentation` once before validating direct
provider previews.

## Removed behavior

The upgrade does not retain compatibility for unscoped global file access,
audit failure followed by business commit, arbitrary request IDs, unchecked
redirect destinations, a separate migration/bootstrap operations flow,
critical in-process goroutines, or parallel handwritten/generated API
contracts. The API now applies Goose migrations and synchronizes access drift
before it reports ready.

Unversioned health probes are now `/health/live` and `/health/ready`; the
existing `/api/v1/health/*` routes are compatibility aliases. External business
APIs remain under `/api/v1`.

## Rollout order

1. Back up and restore-test the database and object store.
2. Build API, worker, and web artifacts from one commit and module fingerprint.
3. Roll out one API instance with its durable installation volume and
   DDL-capable runtime DSN; require `mode=application` and readiness after its
   automatic migration and access synchronization.
4. Start workers only after that API initialization succeeds; workers never
   mutate schema.
5. Roll out the generated-client web build and route `/api/*` plus `/health/*`
   to Go, with all other paths routed to Next.
6. Require the release verification matrix before routing real users.

Use expand/migrate/contract phases for destructive application schema changes.
Rollback to an older unpublished Aginex binary is not guaranteed. Prefer a
forward fix; otherwise restore the database, installation file, and object-store
snapshot captured from one known-compatible baseline as a recovery unit. Do not
assume any destructive migration has a safe automatic down path.
