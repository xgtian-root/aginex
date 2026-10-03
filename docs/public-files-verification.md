# Public file references: verification and release status

Verified locally on 2026-10-03. The matching framework source is published;
CLI release verification follows the procedure linked below.

## Completed checks

| Surface | Result |
| --- | --- |
| Server | `go -C server test ./...` passed |
| CLI | `go -C cli test ./...` passed, including an independent generated consumer |
| Static checks | Server and CLI `go vet ./...` passed; all 9 Agent Skills validated |
| Race detector | `go -C server test -race ./framework/uow ./internal/platform/files` passed |
| Admin | `pnpm check:admin`, `pnpm test:admin` (144 tests), and `pnpm build:admin` passed |
| Contracts | Canonical OpenAPI and TypeScript client regenerated; scaffold snapshot synchronized and drift check passed |
| SQLite | Real baseline migrations, reference behavior, concurrency, and transaction rollback passed |
| PostgreSQL 14.20 | Core up/down/up, Files migration matrix, reference concurrency/rollback, and worker composition passed |
| MySQL 8.4 | Core up/down/up, Files migration matrix, reference concurrency/rollback, and earlier repeatable-read snapshot tests passed |
| Repeatability | PostgreSQL/MySQL Files migration matrix passed two consecutive runs |
| Release development smoke | Six-platform CLI bundle build and native darwin/arm64 smoke passed using the local backend checkout |

The file tests cover two owners sharing a file, full-set replacement,
deduplication, clearing references, non-ready states, business/audit failures,
savepoint rollback after a late write failure, and invalid/escaped transaction
handles. Each database runs concurrent first bindings, replacement versus
clear, owner exchanges, and twelve bind-versus-delete races. PostgreSQL and
MySQL also test a replacement after an earlier transaction snapshot.
All three databases test cancellation after a partial reference change, both
with a normal transaction handle and a handle carrying the canceled child
context: the savepoint restores the old set even when the outer write handles
the error and commits its business update and audit.

HTTP and worker regressions cover `409 FILE_IN_USE`, no deletion state or job
enqueue while referenced, all cleanup payload versions, retries, pending
expiry, multipart cancellation, and stale orphan payloads. API composition is
tested with and without Files. PostgreSQL worker tests verify that enabling
Files before API migrations fails without creating tables, and that migrated
workers receive the service and cleanup handlers.

A PostgreSQL concurrency regression also verifies that late upload rejection
cannot overwrite a file that another transaction has confirmed and bound.
Unexpected references on a pending upload prevent rejection and cleanup
enqueue. Both regressions passed two consecutive runs.

The generated consumer imports only public Aginex packages. It tests the
CAPTCHA/session protocol, metadata/stream reads, shared references, deletion
errors, rollback, and a composition without Files. The development smoke also
compares the generated backend's OpenAPI with the admin template's contract.

## Storage scope

Local storage uses actual temporary directories. The same key exists in A
and B with different contents; after selecting B, opening A's file returns
A's bytes. Missing, unbound, and unusable original profiles fail without
falling back to B. Configuration regression tests verify that default or
explicit `AGINEX_STORAGE_LOCAL_ROOT` values do not overwrite saved directories.

Public `Open` tests use the real S3/OSS adapters and SDKs with controlled HTTP
responses to verify the original bucket/key, authenticated requests, and zero
requests to B when A is missing or unavailable. Existing SDK adapter tests
cover signing, multipart operations, opaque ETags, and applicable
presentation/CORS behavior. These tests do not establish compatibility with
a live vendor.

Real MinIO/S3-compatible and Alibaba OSS environment tests remain unverified
locally: the MinIO image could not be retrieved from Docker Hub or Quay, and
no dedicated OSS test account was configured. AWS S3 and Cloudflare R2 live
environments were not tested. Remote CI was not run. The CI configuration
retains its provider contract checks and now provisions separate databases
for the file and worker integration tests.

## Reproducing database checks

Use disposable databases. The existing migration matrix resets its configured
database, so do not point it at application data or the new Files test database.

```bash
# Core/bundled migration matrices use AGINEX_TEST_POSTGRES_DSN and
# AGINEX_TEST_MYSQL_DSN. Run these packages sequentially when sharing their DB.
go -C server test ./internal/platform/migrate -run TestDatabaseMigrationMatrix -count=1
go -C server test ./internal/app -run TestBundledModuleMigrationMatrix -count=2

# Dedicated databases, separate from the destructive matrices:
# AGINEX_FILES_TEST_POSTGRES_DSN and AGINEX_FILES_TEST_MYSQL_DSN
go -C server test ./internal/platform/files -count=1
go -C server test ./internal/app -run 'TestUploadRejectionPreservesUnexpectedPendingReferences|TestPostgresLateUploadRejectionCannotInvalidateReferencedReadyFile' -count=2

# Dedicated PG database; each test creates and removes its own schema:
# AGINEX_FILES_WORKER_TEST_POSTGRES_DSN
go -C server test ./internal/worker -run TestFilesWorkerServicesAndReadOnlyMigrations -count=1
```

## Release status

`BackendVersion` is `v0.0.0-20261003123916-929bc9e71b8e`, resolved by Go from
published source commit `929bc9e71b8ef3b1ea5291225c9d2d585267b5c4`. It includes
both the CAPTCHA protocol and public Files service required by the scaffold.

CLI `v0.1.2-dev` uses the executable and Go command package `aginex-cli`.
Its release smoke must verify real `GOWORK=off` downloads, backend builds,
OpenAPI, CAPTCHA login, and Files contracts without any local `replace`,
following [the release procedure](cli-release.md). The `--aginex-path` option
remains available for explicitly testing local source.

See [the public API and derived-module example](public-files.md) for usage,
authorization, lock ordering, storage, and baseline migration contracts.
