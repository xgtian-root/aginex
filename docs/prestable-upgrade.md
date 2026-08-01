# Pre-stable remediation upgrade

This is a deliberately breaking upgrade from the 2026-07-30 `main` baseline.
Aginex has not published a stable API, so insecure prototype behavior is
removed instead of being preserved as a compatibility mode.

## Required application changes

1. Define the complete compiled-in module set once with
   `application.Define`. Reuse that `Definition` for API, worker, migrate,
   bootstrap, and OpenAPI entry points. Compare its fingerprint across deployed
   processes. An empty definition no longer includes the sample products,
   dashboard, or file-object API. Existing starter deployments must opt in:

   ```go
   application.Define(
       application.FilesModule(),
       application.StarterExampleModule(),
   )
   ```
2. Run the explicit migrate process before API or worker rollout. Neither
   runtime calls `AutoMigrate` or modifies schema. Use `migrate status` and
   `migrate version` as release checks.
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
8. Use the public `framework/storage.ObjectStore` contract. Object keys remain
   private metadata; signed URLs must not be logged, audited, or placed in an
   idempotency replay record.
9. If the deployment exports telemetry, create one
   `observability.Recorder`, attach it with `Definition.WithObservability`, and
   implement a concurrency-safe deployment sink. Do not install global
   telemetry providers from a reusable module.

## Bundled-schema adoption

The core Goose history remains continuous through version 8 so an already
migrated pre-release database is not reported as being ahead. On a fresh
database, the historical product/file steps are now no-ops and the core never
creates `products` or `file_objects`.

`FilesModule` and `StarterExampleModule` use isolated Goose history tables.
Their first migration uses create-if-missing semantics: an existing pre-release
`products` or `file_objects` table is adopted without copying, dropping, or
rewriting its data, while a fresh installation receives the same final schema.
The adoption migrations deliberately have no destructive automatic down step,
because a module rollback cannot safely distinguish an adopted historical
table from one it created. Remove those tables only through an explicit,
backup-verified application data-retirement migration.

Run the new migrate artifact once before starting API or worker processes.
`migrate status`, runtime startup, and readiness include only the migration
bundles in the selected definition. A zero-business definition therefore does
not require either bundled history; the checked-in starter definition requires
both.

In production, registering `FilesModule` also requires
`AGINEX_JOBS_DRIVER=postgres` so file deletion and orphan recovery remain
durable. A definition without the files module may keep jobs disabled.

## Removed behavior

The upgrade does not retain compatibility for unscoped global file access,
audit failure followed by business commit, arbitrary request IDs, unchecked
redirect destinations, production startup migration, critical in-process
goroutines, or parallel handwritten/generated API contracts.

Unversioned health probes are now `/health/live` and `/health/ready`; the
existing `/api/v1/health/*` routes are compatibility aliases. External business
APIs remain under `/api/v1`.

## Rollout order

1. Back up and restore-test the database and object store.
2. Build API, worker, migration, and web artifacts from one commit and module
   fingerprint.
3. Run the new migration image once and require a current status.
4. Run explicit bootstrap only when synchronizing registered permissions or
   initial administrator access.
5. Roll out workers, then API, then the generated-client web build.
6. Require readiness and the release verification matrix before routing real
   users.

Use expand/migrate/contract phases for destructive application schema changes.
Rollback may require rolling application binaries back before a later contract
migration; do not assume every destructive migration has a safe automatic
down path.
