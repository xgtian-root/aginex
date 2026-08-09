# Aginex v1 Implementation Plan

## Current Goal: Cool-Tech Setup Refinement (2026-08-09)

Shift the completed Setup redesign from a warm editorial commissioning desk to
a cool, precise aerospace-instrument visual language without changing its
workflow, semantics, localization, or responsive behavior.

### Cool-Tech Refinement Phases

| Phase | Status | Exit criteria |
|---|---|---|
| C1. Palette and typography audit | complete | Warm tokens, typography overrides, semantic states, and contrast-sensitive surfaces are mapped |
| C2. Visual-system implementation | complete | Ice-blue neutrals, midnight structure, restrained cobalt signal, technical grid, and geometric type are applied cohesively |
| C3. Verification and delivery | complete | Formatting, Web checks/tests/build, English/Chinese responsive browser QA, and diff checks pass |

### Cool-Tech Guardrails

- Avoid the generic AI-tech formula: no cyan glow on black, purple-blue
  gradients, gradient text, glassmorphism, or monospace-as-technology shorthand.
- Keep Setup intentionally light with a dark structural rail; use cool tinted
  neutrals, one rare cobalt accent, and existing semantic success/danger colors.
- Preserve every state and accessibility contract validated by the completed
  modern Setup redesign.

## Current Goal: Modern Setup Page Redesign (2026-08-09)

Redesign the existing three-step Setup experience with a distinctive modern
visual system while preserving every runtime probe, database verification,
administrator validation, initialization, localization, and recovery path.

### Setup Redesign Phases

| Phase | Status | Exit criteria |
|---|---|---|
| D1. Baseline and visual audit | complete | Current Setup states, component structure, CSS dependencies, screenshots, and active drift are mapped |
| D2. Design direction and implementation | complete | A cohesive modern direction is implemented without altering Setup contracts or state transitions |
| D3. Responsive and interaction hardening | complete | Narrow/wide layouts, keyboard focus, reduced motion, loading/error/success, and both locales are polished |
| D4. Verification and delivery | complete | Focused tests, `pnpm check:web`, `pnpm build:web`, browser QA, and diff checks pass |

### Setup Redesign Guardrails

- Preserve the in-progress Setup and internationalization logic; keep this a
  focused presentation-layer redesign unless a semantic markup adjustment is
  necessary for accessibility.
- Reuse the current Next.js, TanStack Query, Lucide, and next-intl stack; do not
  introduce another design system or state dependency.
- Use a light, editorial-industrial modern direction with warm tinted neutrals,
  one rare high-chroma signal color, asymmetrical composition, and purposeful
  motion that respects `prefers-reduced-motion`.
- Maintain visible labels, 44px targets, `:focus-visible` treatment, localized
  long-copy resilience, and all probe/initialization failure recovery actions.

## Current Goal: Web Internationalization (2026-08-09)

Add production-ready, extensible web internationalization with English and
Simplified Chinese, browser-language negotiation, a persistent user-selectable
locale, localized formatting and error presentation, and regression coverage,
without disturbing the in-progress embedded Setup changes.

### Internationalization Phases

| Phase | Status | Exit criteria |
|---|---|---|
| I1. Baseline and contract | complete | Rendering boundaries, UI copy, errors, tests, and active workspace drift are mapped |
| I2. Locale infrastructure | complete | Typed locale/messages API, negotiation, persistence, document language, and formatter helpers exist |
| I3. Product UI migration | complete | Login, Setup, shell, navigation, and workspace pages use localized messages with a discoverable switcher |
| I4. Verification and hardening | complete | Unit/type/lint/build and focused browser-level coverage pass; long-copy and hydration edges are checked |

### Working Internationalization Decisions

- Ship `en` and `zh-CN` first; adding another locale must be a message-catalog
  change rather than a component rewrite.
- Keep API problem `code` values language-neutral and translate their user-facing
  presentation in the web client; do not localize wire contracts.
- Use an explicit locale cookie as the durable preference, then browser
  `Accept-Language`, then English as the fallback.
- Use the repository-designated `next-intl` runtime, while retaining unprefixed
  routes and repository-owned message catalogs.
- Preserve all unrelated dirty-worktree changes and adapt to the current Setup
  implementation instead of replacing it.

## Current Goal: Embedded One-Time Setup (2026-08-09)

Implement a fail-closed two-mode backend runtime: a fresh installation exposes
only Setup, while a configured installation automatically migrates,
bootstraps, and serves the application. Setup configures the database and first
administrator, persists a sealed runtime configuration, and hot-switches the
same HTTP process into application mode.

### Setup Implementation Phases

| Phase | Status | Exit criteria |
|---|---|---|
| S1. Baseline and architecture seams | complete | Existing config, lifecycle, routes, contracts, web guards, images, and tests mapped; user decisions preserved |
| S2. Installation state and backend supervisor | complete | Strict config-state store, setup/application router isolation, async initialization, auto migrate/bootstrap, and hot swap implemented |
| S3. Contracts and frontend onboarding | complete | Union OpenAPI/client plus guarded three-step Setup UI implemented with fail-closed unknown state |
| S4. Delivery migration | complete | CLI/image/scripts/docs updated for main-owned migrations, persistent config volume, and trusted-network deployment |
| S5. Verification and hardening | complete | Focused regressions and complete Go/Web/contract/image-relevant gates pass; unavailable live dialects reported |

### Locked Setup Decisions

- Missing database configuration means Setup; any partial, corrupt, unsafe, or
  previously sealed configuration fails closed and never implies Setup.
- Setup is embedded in `cmd/server`, works online behind deployment-owned
  trusted-network controls, and intentionally has no application Setup token.
- One persisted high-authority DSN owns runtime DML and automatic DDL migrations.
- The Setup UI covers database plus first administrator only; it generates a
  Session secret when one is not externally supplied.
- Successful initialization atomically persists configuration and hot-swaps the
  handler; no restart or application reset endpoint exists.
- The shipped backend is single-instance. Worker startup remains non-mutating
  and follows API readiness.
- Write-oriented migrate/bootstrap CLI paths are removed; the main server owns
  startup migration and permission synchronization.

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
| First mobile Chinese QA script reached the administrator step but referenced `document.fonts` from the Node context before screenshot capture | 1 | Wait for fonts inside `page.evaluate`, then repeat the same isolated browser path; the Setup installation was not submitted or sealed |
| Initial cool-tech CSS check reported seven descending-specificity warnings for the route-scoped Chinese heading override | 1 | Move the higher-specificity locale override after all base and accessibility selectors, then rerun the focused check |
| Initial Setup redesign Biome check reported formatter-only differences in the new stylesheet and two section opening tags | 1 | Run Biome format only on the five touched Setup files, then rerun the read-only check |
| Isolated fresh-install API initialized Setup mode but stopped immediately when binding the sandboxed loopback port | 1 | Re-run the same explicitly scoped local preview server with loopback bind approval; keep all persistent paths under the unique `/private/tmp` directory |
| The bundled screenshot helper does not implement a conventional `--help` path and rejected the request because `--output` was missing | 1 | Inspect the local script option declarations directly, then invoke it with explicit URL, output, and viewport arguments |
| Bundled Puppeteer dependencies were present but its pinned downloaded Chrome binary was absent | 1 | Reuse an installed system Chrome executable through Puppeteer's supported executable-path environment rather than downloading another browser |
| System Chrome was found, but the sandbox blocked Puppeteer from launching the GUI process | 1 | Re-run the explicit screenshot helper with scoped GUI approval against the loopback-only preview |
| Approved headless Chrome launched, but the isolated Web port refused the connection | 1 | Polling showed Next dev correctly refused a second dev instance because the user already has one on port 3000; preserve it and use an isolated production preview on 3309 instead |
| Final scope assertion chained a successful “no stale `setup.css` references” search whose expected exit code is 1, causing the shell gate itself to report failure | 1 | Re-run the assertion with an explicit inverted `if rg ...; then exit 1; fi` condition; tests and preceding diff/artifact checks were unaffected |
| Initial internationalization planning patch expected the findings title `# Findings & Decisions` | 1 | Inspected the existing planning-file headers and reapplied against `# Findings` without replacing prior content |
| Unquoted zsh path `apps/web/app/(workspace)/layout.tsx` expanded as a glob | 1 | Quote all App Router route-group paths in subsequent shell inspection commands |
| Agent wait requested 1 second, below the collaboration tool's 10-second minimum | 1 | Use bounded waits of at least 10 seconds; no task work was affected |
| API-code inventory command mixed shell quotes around a regex and zsh reported an unmatched quote | 1 | Split the inventory into simpler single-quoted searches without embedded quote classes |
| Internationalization findings patch placed same-file hunks out of source order | 1 | Reapply task-plan hunks in top-to-bottom file order, then patch the other logs |
| Sandboxed pnpm selected the workspace-local store instead of the store backing existing `node_modules` | 1 | Re-run the scoped `pnpm --filter @aginex/web add next-intl` with approved access to the existing pnpm store; install completed |
| Initial installed-type search included a non-existent `apps/web/node_modules/use-intl` path | 1 | Follow the `next-intl` pnpm symlink and inspect the actual `node_modules/.pnpm/use-intl@4.13.5...` type package |
| New jsdom component test could not resolve the Next `@/` alias because Vitest had no config | 1 | Add a minimal React Vitest config mirroring the app-root alias and keep Node as the default environment |
| Locale-switcher component tests accumulated DOM between cases because Vitest globals did not register Testing Library auto-cleanup | 2 | Register explicit `afterEach(cleanup)` in the isolated jsdom test file |
| First complete Web gate passed typecheck but Biome reported safe import/format fixes, a descending-specificity Chinese table override, and deliberate `document.cookie` compatibility usage | 1 | Move the locale-specific table rule after its base selectors, document the allowlisted cookie boundary, and apply Biome only to the reported internationalization files |
| Sandbox denied binding the production Web smoke-test port | 1 | Re-run the local-only `next start` with scoped approval on `127.0.0.1:3301`, verify three locale requests, and terminate the temporary server |
| First commit-safety search embedded both quote styles in one zsh regex and failed before scanning | 1 | Re-run the read-only scan with NUL-delimited paths and simpler patterns; no private keys, AWS access keys, or SSH public keys were found |
| Bootstrap drift test compile compared `string` to `authz.GrantScope` | 1 | Convert the enum explicitly at the SQL DTO boundary, then rerun the focused packages |
| Combined planning/code patch missed the historical error-table separator | 1 | Inspect the exact table shape and apply a targeted planning-only patch |
| Administrator regression referenced a non-existent disabled identity constant | 1 | Use the persisted `"disabled"` status value; production only defines an active constant |
| Setup supervisor fault-injection test referenced `io` without importing it | 1 | Assigned the active Setup owner to add the missing test-only import before rerunning its package |
| System Ruby 2.6 rejected the newer `YAML.load_file(..., aliases:)` keyword during final workflow syntax validation | 1 | Re-run the read-only parse with the Ruby 2.6-compatible single-argument API; the workflow does not require YAML aliases |
| Historical pre-stable upgrade prose still referenced the removed migrate artifact and `migrate status/version` commands | Final repository search | Rewrite that rollout section for API-owned automatic Goose migrations and worker-after-readiness startup |
| New HTTP Setup credential-replacement regression referenced an audit alias not imported by the existing test package | First focused compile | Keep the test at the public string boundary (`Source: "http"`) used by neighboring regressions, then rerun focused race tests |
| New administrator credential-hash query returned a boolean from its slice-returning helper error path | First focused compile | Return `nil, error` from the internal hash list helper, then rerun password/app/framework/server packages |
| Final adversarial review found a marker-appearance TOCTOU and active-candidate Shutdown result loss | Post-gate read-only audit | Recheck the destination after every pre-publication filesystem failure, and retain one shared active cleanup state/result across concurrent and retried Shutdown calls |
| A server test cleanup used `t.Context()` after the testing package had canceled it | First server run after the shared active-cleanup barrier | Give test cleanup its own bounded background context; production callers continue to control only how long they wait for the shared cleanup |
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
