# Progress

## 2026-08-09 — Cool-Tech Setup Refinement

- Loaded `frontend-design`, its color/typography references, the Aginex
  `add-admin-page` guidance, and resumed the existing file-backed task record.
- Locked a polar aerospace-instrument direction that avoids generic neon-dark
  AI styling and preserves the completed Setup interaction architecture.
- Started C1 palette and typography audit.
- Completed C1: mapped centralized warm tokens, the hard-coded warm shadow,
  editorial display stack/oldstyle numerals, and the global Chinese Songti
  override. C2 implementation is in progress.
- Applied the cool semantic palette, technical grid, condensed display stack,
  upright tabular numerals, tighter radii, and scoped Chinese sans typography.
- The first focused CSS pass formatted cleanly but reported cascade-order
  warnings for the locale override; moved it after the base selectors before
  revalidation.
- Focused CSS validation and the first cool-tech production build pass. Desktop
  browser capture is clean; the first mobile Chinese capture reached step two
  before a QA-script-only `document` context mistake, which is being repeated
  without changing application code.
- Corrected mobile capture passes: 390 px viewport/scroll widths match, controls
  retain 44–51 px touch targets, and the scoped PingFang heading override is
  active. One CJK headline wrap and one unidentified 404 resource remain for
  the final polish pass.
- Completed C2 after giving the mobile/tablet Chinese rail headline its natural
  width; rebuilt capture measures the full title on one line with no overflow.
  The only 404 is the unrelated existing `/favicon.ico`. C3 final Web gates are
  now in progress.
- Completed C3: `pnpm check:web`, all 93 Web tests, production `pnpm build:web`,
  standalone artifact verification, focused stylesheet checks, stale-reference
  scan, and final diff whitespace validation all pass. The isolated API/Web
  preview processes were terminated after desktop and mobile Chinese QA.

## 2026-08-09 — Modern Setup Page Redesign

- Loaded the explicitly requested `frontend-design` skill and all linked
  typography, color, spacing, motion, interaction, responsive, and UX-writing
  references.
- Loaded the project-mandated `add-admin-page` guidance and the
  `planning-with-files` workflow, then recovered the existing shared task logs
  without replacing prior Setup or internationalization work.
- Confirmed the worktree is heavily modified and that the Setup page is an
  untracked, in-progress feature; the redesign will preserve that ownership and
  constrain edits to the relevant presentation surface.
- Started phase D1 by locating the Setup page, wizard, and stylesheet and
  recording a modern editorial-industrial visual direction.
- Completed three parallel read-only audits of component behavior, CSS/global
  coupling, and test/E2E constraints. Phase D1 is complete; D2 implementation
  is now in progress.
- Baseline focused Setup-adjacent tests passed (3 files, 18 tests) before any
  presentation changes.
- Added the new commissioning stylesheet, switched all Setup route-state imports,
  removed the superseded paper-ledger stylesheet, and added safe semantic hooks
  (`data-step`, section labels, busy/pressed/current states) without touching
  setup state logic.
- First focused Biome check reported formatter-only differences; no semantic
  diagnostic was emitted. Formatting and revalidation are next.
- Formatted the touched Setup files and passed the focused check plus
  `git diff --check`.
- Completed D2. The new mobile-first commissioning visual system, container
  query, safe-area handling, explicit light-theme primitive remapping, 44px+
  controls, stronger border contrast, reduced motion, forced colors, hover
  capability queries, and responsive initialization states are implemented.
- Passed `pnpm check:web`, all 93 Vitest tests, and the production Web build.
  Phase D3 browser and narrow/wide visual QA is in progress.
- The isolated API reached Setup mode with the intended temporary config path,
  then the sandbox denied its loopback bind. The next attempt uses scoped port
  approval rather than changing any runtime/configuration assumption.
- Started the isolated API on `127.0.0.1:18089` and Next preview on
  `127.0.0.1:3309`. The screenshot helper has no standard help output, so its
  local option declarations will be inspected before capture.
- The first capture attempt found Puppeteer installed but its pinned downloaded
  Chrome missing. Visual QA will reuse the existing system browser if present;
  no browser download is necessary.
- System Chrome exists at the standard macOS path; its first Puppeteer launch
  was blocked by GUI sandboxing, so the capture will be retried with scoped
  approval rather than altering browser dependencies.
- Approved headless Chrome launched successfully, but the isolated Web port had
  stopped accepting connections. Both server sessions will be polled before a
  targeted restart; the browser setup itself is now verified.
- The Web cause is now known: Next protects the workspace from concurrent dev
  instances, and a user-owned dev server already runs on port 3000. It will not
  be interrupted; visual QA will use a separately built production preview on
  port 3309 against the isolated API.
- Completed real-browser desktop and 390×844 mobile QA across English database,
  verified connection, English administrator, and Chinese administrator states.
  All measured states have no horizontal overflow, 44px locale controls, and
  51px actions.
- Confirmed the full-page skip-link appearance was a screenshot stitching
  artifact; real viewport state is correct. The only 404 was optional
  `/favicon.ico`, not a Setup or API failure.
- Exercised step 3 with browser-only mocked initialization polling, preserving
  the temporary server in Setup mode. Desktop and mobile progress/handoff states
  render correctly with no horizontal overflow.
- Verified dark-system isolation, reduced-motion behavior, and keyboard focus.
  The skip link becomes visible with a 3px outline and focus proceeds to the
  language selector as expected.
- Verified fail-closed mode-query failure: no form is present, retry remains
  available, and the page has no horizontal overflow. Increased only the
  wide-screen probe/loading inline padding after visual review.
- Re-ran `pnpm check:web`, `git diff --check`, and the final production build;
  the standalone server artifact exists. Phase D3 is complete and final scope
  review is in progress.
- Final all-93-test run passed. A combined shell assertion then treated the
  expected no-match exit from the stale-import search as failure; re-run that
  assertion with explicit inverted search semantics.
- Corrected the assertion semantics. Final `git diff --check`, standalone
  artifact verification, and stale stylesheet-reference check all pass. D4 is
  complete and the redesign is ready for delivery.

## 2026-08-09 — Web Internationalization

- Loaded the `harden` and `planning-with-files` instructions and recovered the
  existing task logs.
- Confirmed the worktree contains extensive in-progress embedded Setup changes;
  the internationalization work will remain an isolated additive slice.
- Started Phase I1 with an `en` / `zh-CN` implementation assumption and stable,
  language-neutral API contracts.
- The first planning patch missed the existing `# Findings` title; corrected
  the patch target after a read-only header inspection.
- Mapped the root layout/provider/workspace-shell boundary and recorded that the
  project currently has no i18n dependency; one inspection command exposed a
  zsh route-group quoting error, now avoided by quoting parenthesized paths.
- Completed the first full copy inventory across Login, Setup, runtime failure,
  the shell, dashboard, products, files, users, roles, and audit. Recorded the
  machine-code/user-label boundaries and all current locale-sensitive formatters.
- Audited the web test/build configuration, API error boundary, Playwright
  selectors, root redirects, and physical-direction CSS. The implementation can
  use the existing React/jsdom/Intl stack without new runtime dependencies.
- Confirmed the installed Next 16 server request APIs are asynchronous and
  captured the active web diff before choosing the locale server/client seam.
- Verified the repository's dependency catalog already designates `next-intl`
  as P0 and adjusted the infrastructure decision accordingly; a first API-code
  search had a shell-quoting error and the first log patch had out-of-order
  same-file hunks, both corrected without changing product code.
- Verified the current official App Router setup for unprefixed locales,
  request-scoped configuration, client-provider inheritance, cookie-driven
  switching, and deterministic formatting.
- Completed Phase I1 after parallel UI, API/Setup, and testing audits. Installed
  `next-intl` with the workspace's existing pnpm store and began I2 locale
  infrastructure plus catalog implementation.
- Added the Next plugin, request-scoped locale/message/timezone configuration,
  strict next-intl type augmentation, and a root provider with matching document
  language/direction.
- Added complete English/Simplified-Chinese catalogs, localized metadata, a
  persistent language switcher with component coverage, a stable-code problem
  mapper, and logical-direction/CJK hardening styles.
- Migrated the shell, runtime-unavailable state, Login, ResourceList, Dashboard,
  Users, Roles, and Audit; Setup plus Products/Files migrations are running in
  isolated parallel file sets.
- The first combined focused test passed 55 pure tests but exposed missing
  Vitest alias configuration for the new jsdom component test; added a minimal
  React/alias config before rerunning.
- The second focused run reached all component tests and exposed missing DOM
  cleanup between cases; registered explicit cleanup before the next run.
- Completed I2 and I3: every current Login, Setup, shell, runtime, and workspace
  surface now uses the English/Simplified-Chinese catalogs, explicit locale
  formatting, and stable-code localized error presentation.
- The first complete Web gate passed typecheck and all 93 unit tests. Biome then
  identified only safe formatting/import changes plus two focused style-policy
  findings; verification hardening is in progress before the production build.
- Completed Phase I4. `pnpm check:web`, all 93 Vitest tests, `pnpm build:web`,
  Playwright discovery (four workflows), `git diff --check`, and the standalone
  server artifact assertion all pass.
- Ran a production HTTP smoke against the built application: Simplified-Chinese
  `Accept-Language` produced `lang="zh-CN"`, Chinese Setup metadata and copy;
  English produced `lang="en"`; and an exact English locale cookie overrode a
  Chinese header. The temporary local server was stopped after verification.
- Final repository searches found no ambient `toLocale*` formatting, raw API
  `problem.detail` rendering, hard-coded document locale, untranslated visible
  English copy beyond the Aginex wordmark, or remaining physical-direction CSS
  properties in the migrated surface.

## 2026-08-09 — Embedded Setup

- Loaded `planning-with-files`, `onboard`, `frontend-design`, all required
  frontend design references, and `test-and-debug`.
- Recovered the existing repository plan without unsynced session context and
  confirmed a clean Git worktree before implementation.
- Completed the initial read-only architecture pass across backend startup,
  configuration, migrations/bootstrap, Docker delivery, Next routing, API
  client behavior, and production constraints.
- Started Phase S1; no product code has been modified yet.
- Added bootstrap drift detection and configurable audit execution context;
  the first focused compile found a string/scope enum mismatch, now corrected
  with an explicit boundary conversion before rerunning tests.
- Added an active-administrator readiness query and regression; corrected its
  test fixture to use the schema's literal disabled status because the domain
  intentionally exposes only the active constant.
- Focused backend gate now passes for bootstrap drift, trusted audit context,
  active-administrator readiness, and the public application definition.
- Parallel implementation is active in `internal/setup`, `internal/config`,
  and `apps/web`; shared-file status is being checked before integration edits.
- Exposed callable `App.Ready` over the same required checks used by the HTTP
  probe, enabling Setup to validate database, migrations, storage, and module
  dependencies before sealing configuration. Focused readiness regressions pass.
- Completed Phase S1 and entered S2. The strict installation store now passes
  its package tests and cross-compiles; the shared application initializer
  automatically migrates, drift-bootstraps, verifies an active administrator,
  starts the App, and runs callable readiness before returning a hot-swap
  candidate.
- Added SQLite regressions proving Setup creates a ready administrator-backed
  application, configured restart does not duplicate bootstrap audit, and a
  configured empty database is rejected. Focused `cmd/server` tests pass.
- Wired `cmd/server` to `LoadState`, automatic configured initialization,
  environment-marker sealing, Setup supervisor construction, and lifecycle
  ownership. `cmd/server` and `internal/config` pass; `internal/setup` is
  temporarily blocked by one missing `io` import in its in-progress test file.

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
# 2026-08-09 Setup runtime integration

- Focused server lifecycle tests now pass for fresh Setup, asynchronous initialization, in-process handler activation, environment-marker persistence, configured startup, and shutdown (`go test ./cmd/server -run 'Test(FreshRuntime|ConfiguredEnvironmentRuntime|SetupInitializer|ConfiguredInitializer|ShutdownRuntime)'`).
- Focused config, bootstrap/administrator/readiness, and framework application regressions also pass after the runtime integration.
- `pnpm check:web` initially failed in stale `.next/dev/types/validator.ts`: its cached route constraint only knew `/` after new `/login` and `/setup` layouts were added. Regenerate Next route types before treating this as a source error.
- `next typegen` regenerated route definitions successfully; Web source validation will be rerun against the refreshed cache.
- Web typecheck and Biome validation pass after regenerating Next route types.
- The union OpenAPI document and TypeScript client were regenerated successfully with the embedded Setup contract.
- Hardened Setup writes beyond the shared CSRF default: an allowlisted Referer can no longer substitute for an explicit Origin header. The focused CSRF/database tests pass.
- A lifecycle documentation lookup referenced a nonexistent `framework/module/module.go`; the actual hook contract is in `framework/module/types.go`. No source mutation resulted.
- Frontend final gates pass: generated-client typecheck/Biome, 26 Vitest cases, production Next build, and desktop/mobile Chromium workflow QA. `/`, `/login`, `/setup`, and workspace routes remain dynamic SSR and Setup success performs a hard replacement to `/login`.
- Full repository Go tests pass (`go test ./... -count=1`) after the embedded Setup, automatic initialization, CLI removal, and delivery edits.
- Delivery changes now remove the migrate image/CLI paths and default DSN, persist `/data/aginex-config.json`, migrate CI/E2E to API-owned initialization, and document trusted-network Setup plus same-origin routing. Docker execution remains pending because the local daemon is unavailable.
- Added a committed serial Playwright first-run scenario: it verifies initial Setup mode/no-store, submits PostgreSQL and administrator data through the real wizard, waits for hot activation, confirms application mode and Setup 404/no-store, then continues into the existing authenticated workflows.
- First Playwright discovery succeeded (3 serial cases), while the scoped Biome check requested only mechanical line wrapping in the new test; run the formatter before the next gate.
- The new E2E file was formatted successfully. A live local PostgreSQL E2E run is unavailable (`pg_isready` reports no response on 127.0.0.1:5432); CI now owns that real-dialect execution.
- Port 3000 is already occupied, so the local SQLite browser gate will use isolated port 3100. A read-only `ps -p` inspection was denied by the sandbox; no process was stopped or changed.
- The first local Playwright launch failed before serving because `go run` tried to read the sandbox-disallowed user Go build cache. Rerun with `GOCACHE=/private/tmp/aginex-go-cache`; the isolated installation directory was still absent at failure time.
- With the cache corrected, the API constructed Setup and reached its listen path, then the Playwright-managed process exited immediately with a redacted server error before any browser test ran. Inspect port state and isolate whether this is sandbox listener denial or a stale process before retrying; do not reuse the now-created installation directory blindly.
- Escalated listener permission allowed the real browser gate to run. Its first attempt exposed a test-selector bug only in the SQLite variant: the wizard correctly labels that input `Database path`, while the test always requested `Connection string`. No Setup submission occurred; fix the driver-specific label and rerun with a fresh isolated directory.
- The corrected real browser run proved first-run Setup, hot activation, application mode, Setup 404/no-store, and unauthenticated redirect. The pre-existing admin workflow then hit an ambiguous `Products` heading selector on a genuinely empty database; make it exact and rerun against a fresh installation.
- Began cross-tab CSRF hardening from adversarial review: token issuers now reuse a valid shared cookie, and browser writes clear the in-memory token and retry a CSRF-rejected request exactly once while retaining the same idempotency key.
- Focused Go CSRF/application/setup tests pass. Web typecheck passes; the combined Web gate currently requests one mechanical import-line formatting change in `lib/api.ts` after adding runtime validators.
- The first new API safety unit run failed in Node before reaching the fetch mock because the production client intentionally defaults to a relative same-origin base. Reset/import the module under an absolute test-only `NEXT_PUBLIC_API_URL`; this is a test harness issue, not a runtime request failure.
- API safety tests now pass (30 Web tests total), covering malformed mode rejection and one-time cross-tab CSRF recovery.
- Startup now has context-aware database opening and application construction; schema/storage constructor probes use the total initialization context, MySQL skips unbounded version discovery, and the lifecycle hook contract explicitly marks Start context as call-scoped. Bootstrap credentials are cleared before the long-lived App is built.
- A combined low-severity frontend hardening patch failed context verification because it mixed `error.tsx` and wizard input anchors in one hunk. No file changed; apply the copy, ARIA, and exact-409 fixes as separate patches.
- Setup supervisor hardening is complete: exact outer route/method/preflight allowlisting now guarantees business APIs 404 before security/size middleware; Setup caps are 64 KiB body, 32 KiB headers, and 1h CSRF; delayed commit versus timed-out Shutdown now seals Setup and cleans the candidate exactly once. Race tests pass.
- Context-aware database/app focused Go tests and all 30 Web unit tests pass. Web typecheck passes; Biome requested only mechanical wrapping for the new ARIA markup in `setup-wizard.tsx`.
- The irreversible config boundary is now fail-closed end to end: config classifies EEXIST and post-publication fsync failure as sealed, the server adapter translates that result, and the supervisor closes Setup into an unavailable application surface while cleaning the untrusted candidate. Config/setup/server race suites pass.
- A combined Playwright multi-tab/response-loss patch missed the formatter-adjusted anchors and applied nothing. Re-read the current test, then add retry-safe Setup, two-tab CSRF/focus, and accepted-response-loss coverage in smaller hunks.
- Expanded real E2E passed two-tab database tests, accepted-response loss, hot activation, and primary-tab closure, but exposed that relying only on React Query's focus manager did not refresh a background Setup tab when brought forward in Chromium. Add an explicit window-focus mode refetch and rerun fresh.
- Headless Chromium still did not emit a page `focus` event for `bringToFront`, even with the explicit listener; all preceding assertions again passed. Dispatch the browser focus event explicitly after bringing the tab forward so the E2E deterministically exercises the real listener rather than Playwright's headless focus semantics.
- The deterministic focus run now passes the complete first-install/multi-tab/response-loss path and unauthenticated guard. The old admin workflow exposed a second pre-existing strict-selector ambiguity between the toolbar and form `Create product` buttons; scope the submit action to the form before the next retry-safe run.
- After scoping the product submit action, the configured-restart Playwright run passes all three serial cases (`3 passed`): Setup remains permanently closed, authentication guards still work, and the existing administrator workflow completes against the same installed SQLite instance.
- Final formatting and first repository-wide gates pass after all hardening edits: `gofmt` over modified Go sources, `go test ./... -count=1`, `pnpm check:web`, and `git diff --check` are green.
- The final Web unit suite passes all 30 cases and `go mod verify` reports every module verified. The focused race gate has already cleared config, Setup, and server while the heavier application packages continue.
- The complete focused race matrix passes across config, Setup, server, application, CSRF, and context-aware database opening. `go vet ./...`, the production Web build without `NEXT_PUBLIC_API_URL`, and Playwright discovery of all three serial E2E cases also pass; the guarded routes remain dynamic SSR.
- Regenerating the union OpenAPI and TypeScript client is byte-deterministic: pre/post SHA-256 remains `6e237be6b074bef81203789f6642061f2f0fe84dd5237c6a8c4e4b5883fddfab` and `e4d6f2cd29a88a1d93ae291e1b2520ac4c346b43e0a9bc3317e318bc50b4bec6`; `git diff --check` remains clean.
- Final shell syntax validation passes. The first CI YAML parse used a keyword unsupported by macOS Ruby 2.6 and failed in the validator itself, so it will be repeated with that runtime's compatible one-argument API.
- The Ruby 2.6-compatible CI workflow parse passes. Final source review confirms workers use configured-only `config.Load` and never migrate/bootstrap, while the shipped production composition's Files jobs gate makes non-PostgreSQL Setup fail before configuration is sealed; derived development/test compositions retain SQLite/MySQL.
- Acceptance-matrix review confirms strict corrupt/unknown-version/wide-mode/symlink/partial-environment config coverage, standard no-store 404 isolation on both mode surfaces, explicit-Origin CSRF writes, detached 202 initialization, sealed-conflict fail-closed behavior, and environment markers that contain no DSN.
- Final polish now clamps Setup progress to monotonic stage order, corrects the unavailable page so it never claims configuration was unchanged, and tells operators that the shipped production distribution requires PostgreSQL. The focused Setup/server race gate and scoped Web formatting pass.
- A final stale-command search found and removed the last historical references to the deleted migrate artifact and `migrate status/version`; the pre-stable rollout guide now matches API-owned migration and worker-after-readiness operation.
- Post-polish verification is green again: `go test ./... -count=1`, focused Setup/server race, focused vet, `pnpm check:web`, all 30 Web tests, production Web build, deterministic contract hashes, and `git diff --check` pass.
- Added explicit HTTP-Setup credential replacement so a pre-commit failed attempt can be retried with a changed administrator password without silently retaining the old hash. Its first focused compile found only a missing test alias; the regression now uses the same public source string as neighboring tests and will be rerun.
- Credential-retry regressions now pass under the race detector: HTTP Setup replaces the old unsealed password and audits the change, while ordinary CLI/system drift synchronization still preserves migrated credentials and remains a no-op when current.
- Tightened administrator readiness to reject malformed Argon2 hashes and made HTTP Setup verify the exact submitted email/password after bootstrap. The first focused compile exposed one helper error path returning the old boolean shape; it is corrected to return a nil hash slice and will be rerun.
- Focused password, administrator, framework, and server tests now pass: configured startup requires a structurally valid active Administrator credential, and HTTP Setup must prove that the exact submitted credential logs into that role before it can seal configuration.
- Worker startup now waits without opening the database until a durable marker exists and the API explicitly reports application mode plus readiness; corrupt configuration fails immediately and transient probe failures remain bounded and secret-safe. Focused worker race/vet pass.
- Image smoke now performs a real empty-volume Setup, verifies the hot switch and no-store closure, restarts the API on the same volume, and proves Setup stays closed. Playwright retries are disabled and the first-run test can no longer pass by skipping an already-sealed instance; Docker execution itself remains unavailable locally.
- The merged hardening gate passes under the race detector across password, administrator/bootstrap, framework definition, server, config store, Setup supervisor, worker startup, and worker runtime. Web check, all three Playwright test discovery, and image-smoke Bash syntax also pass.
- A process-level Setup retry regression now proves the full initializer wiring: an unsealed first candidate using password A can be shut down and retried with password B, after which only B verifies. The focused server race test passes.
- The shipped server now rejects SQLite/MySQL in production before connection or migration, while keeping all three drivers for development/test and derived compositions. Focused early-rejection and credential-retry race tests pass.
- Final repository verification passes after all acceptance fixes: full Go test and vet, module verification, focused merged race, Web check and 30 tests, deterministic OpenAPI/client regeneration, production Web build, three Playwright cases discovered, Bash/YAML syntax, and clean diff whitespace. Contract hashes remain unchanged at `6e237be6b074bef81203789f6642061f2f0fe84dd5237c6a8c4e4b5883fddfab` and `e4d6f2cd29a88a1d93ae291e1b2520ac4c346b43e0a9bc3317e318bc50b4bec6`.
- Final adversarial review found no P0/fail-open or secret leak. Its remaining Web/smoke items are now fixed: mode/status JSON rejects unknown fields, and worker image smoke waits for the explicit started log rather than treating an indefinitely waiting process as success. Web tests and Bash syntax pass.
- The two remaining durability/lifecycle review items are in implementation: sealed-conflict cleanup receives an explicit Shutdown barrier, and nested configuration directories are moving from one `MkdirAll` durability assumption to per-level creation plus parent fsync.
- The final durability/lifecycle review items are now fixed. Sealed candidate cleanup is published behind a lifecycle-locked completion barrier so Shutdown can time out and retry without reopening Setup or cleaning twice; nested installation directories are created one level at a time and each new directory entry is persisted by syncing its parent before publication can proceed.
- The post-fix race/vet gate passes for config, Setup supervisor, and server integration; final repository-wide verification is running against the converged tree.
- Repository-wide test/race/vet, Web, contract, build, and syntax gates passed on that tree, but the last read-only adversarial pass identified two additional shutdown/commit TOCTOU blockers before handoff: a marker can appear while the pre-publication directory durability probe fails, and concurrent active-application Shutdown calls do not yet share one cleanup result. Both failure paths are now assigned for regression-backed fixes; completion remains pending until the gates pass again.
- Both final blockers are closed. Every pre-publication filesystem failure rechecks the destination and treats existence or an ambiguous inspection as sealed; active-application cleanup now runs exactly once with an independent bounded context while all callers wait with their own contexts and receive the same sanitized result.
- Final independent static review reports no remaining blocker under the locked single-backend-instance threat model. The converged tree passes full Go test/vet, full Go race, focused post-fix race, module verification, Web check and 30 unit tests, deterministic contract generation, production Web build, Playwright discovery, Bash/YAML validation, and `git diff --check`.
