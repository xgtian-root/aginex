# Aginex v1 Implementation Plan

## Goal

Build the first production-capable vertical slice of Aginex: an Apache-2.0,
Coding-Agent-first admin framework based on Go/Gin and Next.js, with
machine-verifiable contracts and Skill-based development documentation.

## Current Remediation Goal (2026-07-31)

Turn the early vertical slice into a fail-closed, production-capable modular
monolith foundation that POSTA can safely extend. Framework core remains
portable across SQLite, PostgreSQL, and MySQL. Public token authentication,
durable jobs, and idempotency are official opt-in modules rather than mandatory
PostgreSQL dependencies.

### Remediation Decisions

- Keep RFC Problem Details and page-based admin lists; add stable error codes
  and an optional cursor-page contract instead of replacing either convention.
- Typed Go operations are the code-side API declaration; generated OpenAPI is
  the published machine contract and the web app consumes generated types.
- Authorization is `resource:action` plus a query/object scope. `own` and `all`
  are grant scopes; `system` is an actor kind.
- Business writes and successful audit events commit atomically.
- API and Worker processes never mutate schema at startup. A migrate command
  owns schema changes and runtime processes enforce the expected version.
- Built-in permissions and the initial administrator are created only by an
  explicit, repeat-safe `aginex bootstrap` command; all bootstrap writes and
  its CLI/system audit event commit atomically.
- `ResourceDefinition` is build-time/generator metadata, not a reflection-based
  runtime CRUD engine.
- The default framework definition is a true zero-business composition.
  Files are an official opt-in module and Product/Dashboard are starter
  examples; POSTA domain models do not enter core.

### Remediation Phases

| Phase | Status | Exit criteria |
|---|---|---|
| R0. Baseline and architecture contract | complete | Existing behavior characterized; module, operation, actor, authorization, transaction, migration, and optional-module boundaries documented in code/tests |
| R1. Migration and composition split | complete | Pure app composition and OpenAPI generation; explicit migrate/status/version commands; API/Worker only check versions |
| R2. Authorization and atomic audit | complete | Fail-closed operation metadata, SQL-level scope, object checks, transactional writes/audits, request ID validation, two-user regression tests |
| R3. Typed API and generated web client | complete | All external operations have typed DTOs/security/errors; generated client is consumed; no handwritten endpoint DTOs or unrestricted `api<T>` |
| R4. Session and HTTP hardening | complete | CSRF, safe redirects, trusted proxies, request limits, shared limiter contract, production fail-fast, redaction tests |
| R5. Safe storage lifecycle | complete | Owner-scoped list/read/delete/upload, content verification, checksum/metadata, deletion state, durable cleanup registration, provider contracts |
| R6. Optional token, idempotency, and jobs modules | complete | Modules are opt-in; refresh replay defense, route idempotency, SQL job leases/retries/dead state, actor/trace propagation have module-specific gates |
| R7. Operations, release, and full verification | source complete; release qualification pending | Health/readiness, metrics/trace hooks, non-root image, SBOM/security/E2E/compatibility gates, docs and upgrade path are implemented; stable release waits for real CI/provider evidence |

### Remediation Verification

- Narrow regression tests run before broad checks for every phase.
- `go test ./...`, `go test -race ./...`, and `go vet ./...`.
- `pnpm check:web`, `pnpm test:web`, and `pnpm build:web`.
- Empty-install and previous-version upgrade tests for every SQL dialect.
- Local, S3-compatible, and OSS storage contract tests; unavailable live
  providers are reported rather than silently skipped.
- OpenAPI validation, generated-client drift, and breaking-change checks.
- Two-user authorization, audit rollback, CSRF, safe redirect, upload failure,
  job lease/retry, refresh replay, and idempotency conflict tests.

### Final Local Gate Status (2026-07-31)

- Passed `go test ./... -count=1`, `go test -race ./... -count=1`,
  `go vet ./...`, and `go mod verify`.
- Passed `pnpm check:web`, `pnpm test:web`, `pnpm build:web`, contract
  generation/drift checks, Playwright discovery, and all nine Skill
  validations.
- OpenAPI and generated-client output are deterministic. Final SHA-256 values
  are `ecb0b9867dc95f6f143e1c49ed9f9d3110ed5208798776f1d4819859099728e3`
  and `38d1c60837f2cabcb24ee842ee79cc9684481454e11061fb92e08a4f7d5ca21f`.
- Live PostgreSQL/MySQL adoption tests, S3/OSS contracts, real Playwright E2E,
  Docker read-only/SIGTERM behavior, SBOM, and security/image scans remain
  release-environment evidence. The implementation and CI gates exist, but a
  stable release must not be declared until those gates run successfully.

## Product Decisions

- Global developer audience; English-first product UI and public API naming.
- Go/Gin backend and Next.js frontend in one repository.
- Development-time Coding Agent support is the differentiator; no runtime LLM
  agent in v1.
- Single tenant in v1.
- PostgreSQL, MySQL, and SQLite are supported.
- Local, S3-compatible, and Alibaba Cloud OSS storage are supported.
- Apache-2.0 license.
- 1-2 core maintainers; prefer explicit contracts and low dependency count.

## Phases

| Phase | Status | Exit criteria |
|---|---|---|
| 0. Product documentation | complete | PRD, dependency catalog, and docs index exist |
| 1. Repository foundation | complete | Go/Next monorepo, config, CI-oriented commands, license |
| 2. Backend vertical slice | in_progress | Local auth, sessions, RBAC, audit, products, files, health and OpenAPI exist; OIDC and management mutations remain |
| 3. Frontend vertical slice | in_progress | Login, shell, RBAC navigation, products, identity views, audit and file upload exist; edit flows/E2E remain |
| 4. CLI and Agent Skills | in_progress | doctor/check/dev/client generation and 9 validated Skills exist; new/resource generation and compatibility install remain |
| 5. Storage adapters | in_progress | Local contract is tested; S3-compatible and Alibaba OSS implementations compile; live provider tests remain |
| 6. Verification and release hardening | in_progress | Unit/integration/build checks and CI DB services exist; E2E, live providers and release packaging remain |

## Implementation Order

1. Establish repository layout, version/toolchain policy, local development
   configuration, and executable verification commands.
2. Implement a minimal backend module system and a complete auth/RBAC/audit
   slice with SQLite first, while keeping dialect boundaries explicit.
3. Generate OpenAPI from backend types and consume it from the web app.
4. Build the production-quality admin shell and one representative resource.
5. Add CLI workflows and Agent Skills around the working vertical slice.
6. Add storage adapters and run the full database/storage test matrix.

## Required Verification

- `go test ./...`
- Go static analysis and vulnerability scan.
- Frontend typecheck, lint, unit tests, and production build.
- Playwright login/RBAC/CRUD/upload scenarios.
- PostgreSQL, MySQL, and SQLite integration matrix.
- OpenAPI/client regeneration produces no uncommitted changes.
- Skill specification and link validation.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---|---|
| Workspace was not a Git repository | Initial inspection | Treat as a greenfield directory; initialize project files without destructive Git operations |
| Sandbox blocked creation of `.git` | `git init -b main` | Re-ran the scoped command with approval and initialized successfully |
| Go cache and module proxy were unavailable in the sandbox | First `go mod tidy` | Moved Go caches into the project and downloaded dependencies with scoped approval |
| Latest Goose raised the module Go directive above the local v1 baseline | Dependency resolution selected Goose 3.27.3 / Go 1.25.7 | Pin Goose 3.26.x so Aginex remains compatible with Go 1.25.0+ |
| Next.js Turbopack production build produced no progress for over two minutes | `next build` | Stop the stalled process and verify with the webpack builder before treating it as an application error |
| Login page used `useSearchParams` without a Suspense boundary | Webpack production build | Wrap the client login form in Suspense with a stable loading state |
| Server page passed formatter functions into a client resource table | Webpack production build | Mark the three thin resource pages as client entry points |
| Workspace policy treated `.agents` as read-only | Skill initializer creating canonical project Skills | Re-run the required initializer with scoped approval for the project `.agents/skills` directory |
| OSS v2 `HeadObjectResult.ContentLength` is a value, not a pointer | First storage adapter compile | Use the value directly; retain pointer helpers for optional response fields |
| GORM mapped `ETag` to `e_tag` while SQL migrations use `etag` | Local upload integration test | Add an explicit `column:etag` model tag to preserve the migration contract |
| Docker CLI was present but the local daemon socket was unavailable | PostgreSQL/MySQL local integration matrix | Keep the tests and CI services active; report the local matrix as not run rather than claiming it passed |
| Sandbox denied binding the API smoke-test port | First process-level server start | Re-run the local-only server with scoped approval on port 18080 |
| Idempotent bootstrap retained generated IDs after ignored inserts | Second server start against the same database | Clear seed structs before re-querying persisted role/permission records and add a restart regression test |
| `openapi-typescript` output did not match the repository formatter | Full frontend verification | Format the generated client inside the deterministic generation command |
| PostgreSQL/MySQL local migration cases were reported as passing packages while their subtests skipped | Readiness baseline on 2026-07-30 | Record provider/dialect availability explicitly and add gates that fail when a required matrix entry is absent |
| Product write remained committed and returned 201 after `audit_logs` was removed | New atomic-audit regression test | Migrated product create/update/delete to the unit-of-work recorder; the regression now returns an error and proves the product row is rolled back |
| Login returned 200 and retained a new session after `audit_logs` was removed | New session/audit atomicity regression | Added transaction-aware auth service methods and migrated login/logout to the unit of work; session changes now roll back with audit failure |
| `App.New` accepted an empty database by applying migrations during startup | New pending-schema startup regression | Replaced startup `migrate.Up` with read-only `EnsureCurrent`; test fixtures and the CLI own explicit migration execution |
| Pure OpenAPI test treated Huma `Paths` as a struct instead of its map type | First contract-composition test compile | Use direct map lookup on `document.Paths`; do not repeat the invalid `PathItems` access |
| Go test attempted to use the sandbox-blocked user build cache | First migration-lock focused test | Re-run Go verification with the task-scoped `/private/tmp/aginex-go-cache` |
| `go doc` interpreted a symbol as a package and attempted unavailable proxy resolution | Migration-lock API confirmation | Inspect the already-cached Goose source directly; do not use the malformed package/symbol invocation |
| Server compile retained an unused `time` import after timeouts moved into config | First server hardening compile | Remove the stale import and rerun the focused server package test |
| Generated OpenAPI correctly exposed nullable Go slices, breaking web assumptions about list and permission arrays | First generated-client typecheck | Make response-contract slices explicitly non-null, ensure handlers emit empty arrays, regenerate, and update the ApiError test to the complete generated Problem type |
| Web lint found one stale type import, formatting drift, and rejected a control-character regex | First generated-client lint | Remove the stale import, apply repository formatting, and replace the regex with an explicit character-code helper |
| Shared file-authorization request helper had no `testing.T` for CSRF token generation | First CSRF-enabled app test compile | Use a fixed valid 256-bit test token in the shared helper; cryptographic generation remains covered in `framework/httpx` |
| New trusted-proxy integration assertion referenced `domain.Session` without importing the package | First HTTP integration test compile | Add the missing domain import and rerun the narrow tests |
| File intent, confirmation, and list responses serialized `domain.FileObject`, exposing bucket, object key, ETag, and owner ID | New public file DTO regression on 2026-07-31 | Map domain/storage values into explicit `FileResponse`, `SignedRequestResponse`, and typed page contracts |
| Upload intent returned 201 and retained metadata after the audit table was removed | New upload-intent atomicity regression on 2026-07-31 | Persist the intent and its successful audit event through the shared unit of work; regression now proves rollback |
| Upload confirmation returned 200 and retained the ready state after the audit table was removed | New upload-confirmation atomicity regression on 2026-07-31 | Re-read the authorized intent and persist verified metadata plus audit through one unit of work; regression now proves rollback |
| Escalated Go module query was sent using JSON to the JavaScript orchestration tool | First WebP decoder dependency lookup | Reissue the query as a valid `tools.exec_command(...)` JavaScript call |
| Embedded WebP fixture dimensions were assumed to be 80x60 but decode to 75x100 | First storage-verifier unit test | Use the decoder-derived fixture dimensions in the expected metadata |
| Whole-framework regression encountered an in-progress `framework/ratelimit` embed with no migration files | Storage-verifier expansion check during parallel work | Leave the unrelated package to its owning agent; retain passing focused storage test, race, and vet evidence |
| New shared rate-limiter regression suite did not compile because its provider contract had not been implemented | First `go test ./framework/ratelimit -count=1` | Expected red test-first baseline; implement the provider-neutral contract, GORM CAS store, and opt-in migrations before rerunning |
| Rate-limiter corrupt-state test tried to write a zero revision that the migration correctly rejects | First implemented `go test ./framework/ratelimit -count=1` | Corrupt an unconstrained timestamp relationship instead, preserving production database checks while exercising fail-closed provider validation |
| CLI migration status test retained the four-migration pending count after adding file verification metadata | First focused migration/storage integration verification | Update the expected pending count to five and rerun the focused CLI suite |
| Contract generation inherited the sandbox-blocked user Go build cache | First `pnpm generate:contracts` after image and rate-limit contract changes | Re-run the deterministic generator with the task-scoped `/private/tmp/aginex-go-cache` and offline module resolution |
| File authorization regression still expected synchronous physical deletion | First app suite after changing delete to durable scheduling | Inject the test queue after authorization checks and assert `202 deleting` metadata instead of a removed row |
| Durable runner regression suite did not compile because dispatcher, execution-context accessors, and runner APIs did not yet exist | First `go test ./framework/jobs -run 'TestDispatcher\|TestRunner\|TestNewRunner' -count=1` | Expected red test-first baseline; implement the registry-backed dispatcher and bounded lease runner before rerunning |
| Repository-wide Go regression overlapped unfinished token-auth and file-cleanup work from parallel agents | First `go test ./...` after the durable runner passed its focused gates | Do not alter those owners' in-progress packages; retain green jobs/module subtree, repeat, race, and vet evidence, then rerun the repository gate after parallel slices converge |
| Token refresh replay regression did not compile because the optional token-auth contract and store did not yet exist | First `go test ./framework/tokenauth -run TestConcurrentRefreshAllowsOneWinnerAndReplayRevokesFamily -count=1` | Expected red test-first baseline; implement the isolated signed-access and hashed-refresh module before rerunning |
| Token-auth behavior suite referenced `gorm.DB` in a helper without importing GORM | First full `go test ./framework/tokenauth -count=1` | Add the missing test-only import, then rerun the package gate |
| New idempotency regression suite did not compile because its provider-neutral lifecycle and store did not yet exist | First `go test ./framework/idempotency -count=1` | Expected red test-first baseline; implement bounded claims, leases, safe response replay, portable storage, and isolated migrations before rerunning |
| CLI migration status regression retained the five-migration pending count after adding append-only audit protection | First focused CLI check for migration 6 | Update the expected empty-schema pending count to six and rerun the CLI gate |
| Persisted header decoder used a distinct map type that was not assignable to `http.Header` | First idempotency implementation compile | Decode directly into `http.Header` so replay uses the public response type without an unsafe conversion |
| Durable file cleanup returned `already completed` or `record not found` when another delivery completed or removed metadata after physical deletion; GORM also replaced the injected `UpdatedAt` value | First `go test ./internal/platform/filecleanup -count=1` | Treat both post-delete terminal races as idempotent success and update status timestamps with explicit columns so retry and audit semantics remain deterministic |
| Core migration status regressions retained version six after adding user identities | First `go test ./internal/platform/migrate -count=1` for migration 7 | Update exact current/latest/pending expectations and add identity upgrade/rollback regressions before repeating the migration gate |
| Removing credentials from `domain.User` left two test fixtures constructing `PasswordHash` | Parallel app/file-cleanup compile after the identity model change | Create a password `UserIdentity` only for the fixture that logs in, remove the unused credential from the FK-only cleanup fixture, and scan all Go struct literals |
| GORM treated a raw `[]byte` scan target as a slice of scalar rows in the upload replay regression | First focused HTTP idempotency test run | Scan the single BLOB response into a string value before asserting that no signed upload URL was persisted |
| Bootstrap-focused app verification overlapped an in-progress idempotency middleware integration | `go test ./internal/app -run '^TestBootstrap' -count=5` while `modules.go` referenced not-yet-added middleware symbols | Leave the parallel-owned app integration untouched and repeat the bootstrap/app gate after its owner completes |
| Temporary dummy-hash generator was outside Go's `internal` import boundary | First `go run /private/tmp/aginex_dummy_hash.go` | Generate the one-time test constant from a temporary path inside the module, then remove the helper |
| Combined identity/application regression reached an in-progress idempotency test with an invalid SQLite blob scan target | `go test ./internal/app ...` while `TestUploadIntentReplayDoesNotPersistSignedRequest` scanned `response_body` into a scalar byte | Leave the parallel-owned idempotency test to its owner; identity, migration, password, file-cleanup, and CLI packages passed, then repeat the app gate after integration converges |
| Session-revocation HTTP verification overlapped the explicit-bootstrap test-first implementation | Focused `go test ./internal/app ...` while `bootstrap_test.go` intentionally referenced the not-yet-added exported `Bootstrap` entry point | Leave the parallel-owned bootstrap files untouched and repeat the app, race, and vet gates after that implementation converges |
| Explicit-bootstrap regression tests initially failed to compile because the exported entry point did not exist | First `go test ./internal/app -run 'Test(Bootstrap\|NewDoesNotBootstrap)' -count=1` | Expected test-first failure; implement `app.Bootstrap` and remove the startup call before rerunning |
| Bootstrap CLI regression failed with `unknown command "bootstrap"` | First `go test ./internal/cli -run TestBootstrapCommandIsRepeatSafeAndAudited -count=1` | Expected test-first failure; register an explicit environment-backed command that delegates to `app.Bootstrap` |
| A multi-file test-helper patch overlapped the session-revocation agent adding its own explicit Bootstrap call | First attempt to adapt all app fixtures in one patch | Keep the parallel-owned session fixture intact and apply the shared helper plus unrelated fixture changes in smaller scoped patches |
| Worker startup inspection referenced a non-existent `internal/worker/worker.go` | Combined read-only startup inspection | No implementation issue; worker composition uses different filenames, and the bootstrap change is isolated to API composition/CLI |
| Session-revocation fixtures still expected `App.New` to seed permissions and the administrator after bootstrap became explicit | First focused app run after the explicit-bootstrap entry point compiled | Invoke `Bootstrap` explicitly in the isolated session fixture before constructing the read-only runtime app |
| Pending-upload expiry regressions referenced the not-yet-implemented version-two cleanup contract | First `go test ./internal/platform/filecleanup -run 'TestHandleV2' -count=1` | Expected red test-first baseline; add an exact version-two payload, mode-aware handler, and stale-state-safe expiry transition while retaining the version-one handler unchanged |
| Upload-intent integration regressions referenced the not-yet-defined expiry grace and enqueue path | First focused `go test ./internal/app` for pending-expiry scheduling | Expected red test-first baseline; schedule the v2 expiry job inside the existing audited transaction and give deletion causes disjoint idempotency keys |
| The legacy trace regression expected the API to echo the caller's parent span ID | First app run after real server spans replaced trace-string correlation | Assert the response keeps the incoming trace ID but creates a distinct server child span ID; malformed input still starts a fresh valid trace |
| Assigning the observed limiter back to a variable inferred as `*GORMLimiter` failed to compile | First app integration of the provider-neutral limiter decorator | Keep the concrete GORM store in a separate variable and assign the decorator result to the `ratelimit.Limiter` interface |
