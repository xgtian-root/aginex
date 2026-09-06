# Progress

## 2026-09-06 — OSS provider read URLs and durable cleanup

- Loaded the upload, debugging, and persistent planning workflows.
- Reproduced the queue-disabled delete error and verified the uploaded object
  exists at the recorded nested OSS key.
- Locked private provider reads, required customizable HTTPS access origin,
  PostgreSQL durable jobs, and independent worker execution with the user.
- Started O1 by mapping the storage/profile/API/CLI seams and preserving the
  repository's unrelated in-progress backend-to-server rename.
- Completed the implementation seam audit for one-shot and resumable confirm,
  optional storage capabilities, generated routes, and worker startup.
- Added the strict `accessBaseUrl` model and validation, split OSS read/control
  signing, introduced verified presentation finalization, invoked it from both
  upload completion paths, and removed the provider proxy handler from source.
- Replaced the obsolete proxy regression with OSS read-host signing coverage;
  focused config/storage/app tests now pass with a task-scoped Go cache.
- Added the OSS access-origin field to the administration editor/card and both
  locale catalogs, plus a required-URL UI regression.
- Added and tested an audited, re-runnable reconciliation command and
  conditional PostgreSQL worker supervision in `aginex dev`.
- Updated the current private installation with the official bucket access
  origin. Live reconciliation changed one object on the first run and no-op'd
  on the second.
- Restarted the API with PostgreSQL jobs and started the independent worker;
  both are running and ready on the new configuration.
- Re-finalized the existing object with verified MIME and no durable content
  disposition. A live signed read reached the configured provider host with a
  200 response, while a read-only CNAME query proved the remaining inline
  preview blocker is OSS force-download behavior on the official domain and no
  custom CNAME is currently bound.
- Updated the current development `.env` to use PostgreSQL jobs so future
  `aginex dev` runs supervise the worker automatically. Live API deletion was
  not forced because the retained bootstrap administrator credentials are
  empty; the full API acceptance, durable queue, and cleanup handler suites
  cover the deletion state machine without bypassing authentication.
- Completed O5: full server and CLI Go suites, admin type/lint, all 134 admin
  tests, the production admin build, canonical template synchronization and
  drift check, generated-contract proxy-route absence, and `git diff --check`
  all pass. API and worker processes remain running and ready.


## 2026-09-04 — `aginex new` project initialization

- Loaded the repository `create-aginex-project` workflow and the persistent
  file-planning workflow.
- Locked the zero/one-argument target semantics and non-overwrite, staging,
  public-API, and secret-handling guardrails before implementation.
- Started N1 to audit CLI conventions and define the smallest runnable external
  project template.
- Chose a dependency-based hybrid scaffold rather than an embedded full-source
  fork. The audit identified public hosting/Setup coordination as the required
  framework seam to expose before generated projects can match the starter.
- Added the curated embedded asset boundary and verified its root package test.
  The initializer implementation now has stable application-side input without
  accidentally bundling repository internals or local build artifacts.
- Implemented both `new` target modes, strict portable names, optional checked
  Go module paths, deterministic manifests with ownership/hashes, staged
  no-replace publication, and change-aware rollback.
- Added broad CLI/scaffold regressions plus a real external-consumer gate. The
  generated Go module passes readonly tests and a zero-diff tidy check using a
  temporary local framework replacement; the Windows CLI also cross-compiles.
- Updated installation, initialization, CLI contract, roadmap, and project
  creation Skill documentation.
- Exposed public API and worker hosting lifecycles and switched generated entry
  points to those public contracts; no generated Go source imports framework
  internals.
- Completed both real binary smoke modes. The generated project passes
  `doctor`, `check --skip-build`, readonly Go tests, zero-diff `go mod tidy`,
  frozen-lockfile Web installation, Web type/lint checks, and all 132 Web tests.
- Project-owned OpenAPI and TypeScript regeneration is deterministic and its
  post-generation hashes match `.aginex/project.json`.
- Completed N5: repository-wide `go test ./... -count=1`, `go vet ./...`, the
  focused public-host race suite, Windows CLI cross-compilation, Skill
  validation, the production Web build, and `git diff --check` all pass.
- Reopened the initializer phases after final independent review reproduced
  three compileability gaps: source-built CLIs need an explicit local Aginex
  source boundary, `vendor` module path segments must be rejected, and an
  application module must not shadow Aginex or any of its dependency modules.
- Closed all three review findings with regression coverage. A real
  source-built binary now fails without mutation and an actionable
  `--aginex-path` message, then successfully creates and compiles both named and
  current-directory projects when that path is supplied; adversarial module
  names fail without targets or staging residue.
- Re-ran the converged completion gates: full Go tests and vet, race tests for
  public hosting plus CLI initialization, Windows CLI cross-compilation,
  generated external-project readonly tests and zero-diff tidy, CLI help, and
  `git diff --check` all pass.
- The final independent re-review reports no remaining blocker. The generated
  default-module project also cross-compiles its API, worker, OpenAPI, and
  composition packages for Windows.


## 2026-08-11 — PostgreSQL reinitialization ownership repair

- Recovered the exact operator failure: the confirmed local reset failed while
  renaming `public` with SQLSTATE 42501 because the configured role is not its
  owner.
- Confirmed from the command's error path that the private configuration archive
  was restored and the database transaction did not commit.
- Loaded the repository `test-and-debug` workflow and refreshed the persistent
  planning files. Started PR2 to add a regression before changing the archive
  implementation.
- Inspected the sole PostgreSQL migration baseline and confirmed the application
  schema consists of tables plus dependent indexes. Chose a transaction-scoped
  preflight/move/postflight design that leaves `public` untouched.
- Added the object-level PostgreSQL archiver, fail-closed catalog checks, unit
  regression, and Operations documentation. The first focused test invocation
  was blocked only by sandbox access to the user Go build cache.
- Focused CLI regressions pass. The first rollback-only live PostgreSQL probe
  made no persistent changes and exposed one legitimate application function
  that the archive inventory must support before the probe can pass.
- Added supported application-function inventory and movement with ownership
  checks. The second live probe passed in 0.03 seconds and explicitly verified
  both in-transaction archive state and complete post-rollback restoration.
- PR2, PR3, and PR4 are complete. PR5 full relevant verification is in progress.
- Completed PR5: `go test ./... -count=1`, `go vet ./...`, focused CLI
  regressions, the explicit live rollback probe, and scoped `git diff --check`
  all pass. No real reinitialization was committed; the operator can now rerun
  the same confirmed command.


## 2026-08-11 — v1-only configuration and local reinitialization

- Loaded the `test-and-debug` and persistent file-planning workflows.
- Recorded the new v1-only installation policy and safe-reset guardrails before
  changing CLI or configuration code.
- Confirmed from the prior startup probe that configuration-only compatibility
  reaches database migration but cannot make the retained v2 Files history
  compatible with the sole current v1 baseline.
- Completed R1/R2: locked the `dev reinitialize` dry-run/typed-confirmation
  contract, v1-only runtime configuration, local/production rejection, and a
  real SQLite archive regression. The focused config and CLI suites pass.
- Completed R3 and the documentation half of R4. The reset implementation now
  revalidates file identity and target immediately before mutation, rejects
  symlinked/non-private backup roots, and preserves per-dialect database state.
- Ran `go run ./cmd/aginex dev reinitialize` against the actual ignored v2
  marker. It printed only `postgres:aginex@localhost:5432`, the prospective
  backup path, and the exact confirmation command; the marker stayed at v2 and
  no backup directory was created.
- Completed R4. The SQLite execution regression now reopens the archived
  database and verifies retained schema, validates the archived v1 marker and
  credential-free manifest, and confirms the post-reset runtime enters Setup.
  CLI help and all focused config/CLI/API/worker tests pass.
- Completed R5. `go test ./... -count=1`, `go vet ./...`, focused config/CLI
  race tests, `pnpm check:web`, all 132 Web tests, `pnpm build:web`, Skill
  validation, `aginex doctor`, `aginex check --skip-build`, and
  `git diff --check` pass.
- PostgreSQL and MySQL execution paths are implemented with transactional/
  atomic archive boundaries, but were not executed against the user's retained
  databases. The real PostgreSQL marker was exercised only through the
  guaranteed non-mutating dry run; SQLite is the executed recovery contract.

## 2026-08-11 — General file uploads

- Recorded the pre-release/current-only rule in `AGENTS.md` and the upload Skill.
- Added installation-wide pending/runtime file policy settings and the upload
  policy endpoint.
- Replaced the image-only intent/confirmation path with generic streaming file
  verification, extensionless object keys, octet-stream storage, preview-kind
  metadata, and controlled preview/download responses.
- Added resumable intent/session/list/resume/sign/ACK/Local part/complete/cancel
  handlers with 32 MiB parts, persisted opaque ETags, fingerprint matching,
  CAS transitions, and uncertain provider completion recovery.
- Registered the session operations in the module and OpenAPI contract; focused
  compile passes. Existing app behavior tests are now being updated from the
  retired image-only PUT contract.

## 2026-08-10 — People and Role Management Console

- Loaded the persistent planning workflow plus the Aginex business-resource,
  RBAC, custom-operation, and admin-page skills.
- Recovered a clean worktree and preserved the existing project history in the
  shared planning files.
- Started A1 by confirming that current access APIs and pages are list-only,
  permission definitions are code-owned, and Administrator grants are already
  synchronized by bootstrap.
- Confirmed a clean starting tree, the separated password-identity model, and
  the existing transaction-aware browser-session revocation seam. Parallel
  backend, frontend, and verification audits are in progress before the public
  contract is locked.
- Mapped the central `App` composition, auth principal/grant loading, UoW audit
  pattern, operation registry, contract metadata, and current list handlers.
  A1 now focuses on locking explicit DTOs and security transitions.
- Verified all three core migration histories. Existing schema/FKs/scopes cover
  the requested management relationships, so no schema change is planned unless
  implementation tests uncover dialect drift.
- Audited password policy and RFC problem handling. Managed-user passwords will
  preserve the established non-empty/non-template policy without reintroducing
  length limits; domain conflicts and lockout guards will receive explicit
  4xx mappings.
- Audited the current Web shell, generated-client wrapper, Product CRUD pattern,
  responsive styles, and test harness. The new console will reuse query/CSRF/
  localization infrastructure while adding permission-aware actions and
  confirmations missing from the starter Product UI.
- Located the reusable Go integration-test harness and confirmed no framework
  migration or global GORM error-mode change is needed for the first
  implementation.
- Locked the direction for system-managed Administrator grants, explicit
  own/all role grants, delegation ceilings, and transactional self/last-admin
  protections. The remaining A1 work is the exact route/DTO matrix.
- Completed A1 and locked the route, permission, DTO, password, soft-delete,
  role-grant, and Administrator transition semantics. A2 backend implementation
  is now in progress; A3 can proceed against the fixed generated contract.
- Backend, frontend, and documentation implementation are running in parallel
  on disjoint files. Located the deterministic generation and CI-equivalent
  verification commands for the convergence phase.
- Sent the backend implementer the audit-sensitive-field constraint and mapped
  the OpenAPI generator's automatic path/CSRF/idempotency behavior for review.
- Documentation and the RBAC Skill have been updated in parallel; Skill
  validation and diff whitespace checks pass. Final wording will be rechecked
  after code and verification converge.
- Split contract coverage into an independent test task while backend source is
  implemented. Confirmed existing Administrator permission-count assertions are
  registry-driven and should naturally cover the expanded permission set.
- Backend permission/DTO/OpenAPI source and frontend API/shared-management
  primitives are now landing. No generated artifact has been touched before
  source convergence.
- Reviewed the first typed access-management DTO pass: user and role create/
  update payloads, role replacement, password reset, explicit `own|all` grant
  inputs, and credential-free response shapes are represented. Backend service
  and invariant review remains in progress before contract generation.
- Completed the first security pass over the access service and escalated a
  system-role bypass in generic user operations before convergence. Password
  reset is locked to a bodyless 204 contract; the Web helper is being aligned.
  Request-level regression coverage is now being added in parallel.
- People and Roles page implementations, shared responsive/dialog styles, and
  both locale catalogs have landed in source form. Their first action-matrix
  review identified dedicated enable/disable and system-Administrator visibility
  corrections, which are being applied before generated-client type checking.
- The backend now compiles and the new request-level access suite passes,
  including selected grants, forbidden callers, system-role immutability,
  self/last-admin protections, session revocation, and delegation ceilings.
  A full `internal/app` run found one stale pre-existing registry expectation;
  implementation review also required accurate roles/Administrator data in
  `/auth/me` before contract generation.
- The stale built-in registry expectation has been expanded to the five current
  resources. Frontend action matrices and delegation controls are converging;
  one nuanced full-replacement case (preserving an unchanged grant above the
  actor's ceiling) is under backend/frontend contract review.
- Backend and frontend delegation semantics now agree on exact preservation of
  locked grants, authentication responses carry accurate role/scope data, and
  the full `go test ./internal/app -count=1` gate passes. A2 is awaiting the
  independent security review; generated OpenAPI/client convergence is next.
- Regenerated OpenAPI and the TypeScript client from source. Added Huma's
  item-level enum metadata so `allowedScopes` is generated as
  `("own" | "all")[]` rather than unbounded `string[]`; generated artifacts were
  not hand edited. Web type checking can now proceed against the typed contract.
- `pnpm check:web` passes (TypeScript plus Biome), and all 111 Web unit tests in
  12 files pass against the generated contract. A3 source implementation is
  functionally converged; production build and full repository gates remain.
- The optimized Next.js production build passes. Full uncached `go test ./...
  -count=1` and `go vet ./...` also pass with an isolated task-scoped Go cache.
  Remaining gates are race/contract determinism/skills/check/E2E discovery and
  final independent security review.
- Focused access/bootstrap tests pass under the Go race detector. A second
  source generation produced byte-identical OpenAPI and TypeScript artifacts
  (`bd4f07…` and `f702e4…`), confirming contract determinism.
- `aginex skills validate`, `aginex check --skip-build`, and Playwright test
  discovery pass; the consolidated check now reports 112 Web tests. A serial
  People/Access browser workflow is being added before the final E2E gate.
- The serial access browser workflow and current-user retry states are now in
  source and pass Web checks/build/discovery. Two isolated E2E starts did not
  enter a test: the first hit the sandboxed default Go cache, and the second API
  reached Setup on its unique port before the companion Web server failed to
  start (consistent with the live dev server holding Next's shared dev lock).
  A production-server configuration is the remaining non-invasive execution
  path; the user's live services have not been stopped.


## 2026-08-10 — Administrator Password Length Limits

- Loaded `test-and-debug` and the persistent planning workflow, recovered the
  converged structured-Setup worktree, and started a cross-layer constraint
  inventory before changing validation.
- Mapped the constraint into Setup, Argon2 hashing/verification, production
  environment bootstrap, login contracts/UI, OpenAPI, generated types, and
  localized Wizard copy. AP1 is continuing with regression inventory while
  backend and frontend implementation proceed on disjoint files.
- Completed AP1. Confirmed that Setup's 64 KiB and the application's configured
  request-body ceiling can remain as transport protections while all
  password-specific length checks are removed. AP2 backend/contract work and
  AP3 frontend/test work are in progress.
- Completed AP2 and AP3. Backend regressions cover one character, `123456`, and
  a value beyond the old maximum across Setup, production bootstrap, hashing,
  and real login; frontend Setup/Login regressions cover one character and
  1,025 characters. Focused Go packages and 9 focused Web tests pass.
- Regenerated OpenAPI and the TypeScript client. Both administrator creation
  and login password schemas are write-only strings with no length metadata.
  AP4 full verification and generation-determinism checks are in progress.
- Closed the final P3 bootstrap/login consistency gap by validating that the
  configured global request-body limit can carry the exact administrator login
  JSON. A focused config regression confirms the error names only the body
  setting and never includes the password.
- Completed AP4. `go test ./... -count=1`, `go vet ./...`, relevant five-package
  race tests, `go mod verify`, `pnpm check:web`, all 102 Web tests,
  `pnpm build:web`, Playwright discovery, `aginex doctor`, and
  `aginex check --skip-build` pass. Contract regeneration is byte-deterministic
  at the recorded final hashes, formatting and `git diff --check` are clean.

## 2026-08-10 — Local PostgreSQL and MariaDB Verification

- Loaded the Aginex `test-and-debug` workflow and the persistent planning
  workflow because this task mutates two retained local database providers.
- Confirmed PostgreSQL on loopback port 5432 and MariaDB on port 3306, then
  prepared the requested `aginex` database objects without deleting or
  resetting unrelated data.
- Direct credential probes pass. PostgreSQL reports version 14.20 with login
  role/database/owner all `aginex`; MariaDB reports version 12.0.2 with selected
  database `aginex`, `utf8mb4`, and `utf8mb4_unicode_ci`.
- Started an isolated Setup-mode API and submitted the new structured
  PostgreSQL and MySQL database-test bodies. Both returned HTTP 200 and
  `{"status":"ok"}`. The temporary API was stopped and its managed config file
  was never created.
- Provider-specific full Setup runs also reached application mode and readiness
  200. Final read-only queries confirm 17 base tables, Goose version 8, and the
  expected active integration administrator in each retained database.
- PostgreSQL role/database and MariaDB database, tables, migrations, and test
  data are deliberately retained as requested. No repository source file was
  changed by the provider setup/testing commands.

## 2026-08-10 — Structured Setup Database Configuration

- Loaded the file-backed planning workflow and Aginex admin-page contract,
  accessibility, responsive, and verification guidance.
- Confirmed the repository starts clean on `main` and added a four-phase plan
  without replacing the prior Setup implementation history.
- Started DB1 audit of backend request/validation/configuration seams, frontend
  driver-specific form state, generated OpenAPI types, and regressions.
- Located the current raw-DSN contract and confirmed it is also reused as the
  internal installation/runtime value. Began designing a separate structured
  wire DTO with a single conversion boundary rather than weakening persisted
  configuration semantics.
- Confirmed existing SQLite directory creation and managed-installation DSN
  persistence can remain unchanged; only the public Setup DTO and conversion
  path need to become structured.
- Audited the wizard's raw-DSN state, normalization, verification fingerprint,
  retry path, localization, and direct API-test/E2E dependencies. The existing
  exact-tested-configuration invariant can be retained with structured state.
- Confirmed `github.com/go-sql-driver/mysql` is already a direct dependency and
  provides the canonical DSN formatter. Logged and corrected an optional-glob
  inspection error without changing source files.
- Mapped Supervisor detachment, credential clearing, candidate readiness,
  installation commit, and fail-closed activation. Chose the HTTP handlers as
  the single structured-input-to-runtime-DSN conversion boundary.
- Identified the reusable form CSS and generation/check commands, plus the E2E
  compatibility seam where environment DSNs must be decoded only by the test
  harness into browser-visible structured fields.
- Completed DB1 with independent backend, frontend, contract, test, deployment,
  and documentation audits. Locked the nested per-driver wire contract and
  started backend and frontend implementation in parallel.
- Confirmed Web Setup request aliases currently depend on generated schema
  names and will need a small post-generation update for the renamed completion
  payload. Logged a second optional-glob inspection error and switched to
  explicit-path searches.
- Structured PostgreSQL E2E variables, SQLite image-smoke requests, API safety
  fixtures, and browser-versus-environment DSN documentation are implemented;
  focused formatting/syntax verification is still pending the agent handoff.
- Completed the first cross-layer code review. Requested required database
  password enforcement and an explicit trailing-directory normalization test;
  verified MySQL round-trip safety, structured frontend submission, exact-test
  invalidation, and responsive form-grid integration.
- Backend and frontend owners completed their focused suites. The first
  repository contract generation was blocked before writing output by the
  sandbox-disallowed user Go build cache; retrying with the established
  task-scoped `/private/tmp` Go cache.
- Completed DB2 and DB3. Contract regeneration succeeded with the task-scoped
  cache; its one expected Web alias error was fixed from the removed internal
  schema name to `SetupCompleteInput`. DB4 full verification is in progress.
- First combined gate passed Web check and all 98 Web tests. The focused Go
  server suite correctly rejected one stale process-level test fixture still
  posting a raw DSN; that fixture now uses the structured public SQLite body.
- Full `go test ./...`, `go vet ./...`, and the production Web build pass.
  OpenAPI/client regeneration is byte-deterministic at hashes
  `e435c0b203b8bd7a723f46f436725fc5c244a066ddbf6d3de5e2c83e1094a787`
  and `c8fef6c231639dcfaa15546c18a7588beffaa47736d2ae5f721c4f20f36d4723`.
- Attempted real responsive browser QA with an isolated fresh Setup API. The
  existing workspace dev lock was preserved, and the in-app browser's local URL
  safety policy blocked the recovered preview refresh; browser QA ended without
  switching surfaces. Automated component, E2E discovery, type, lint, and
  production-build coverage remain green.
- Focused Setup/server race tests, module verification, E2E discovery (4 tests),
  Bash syntax, CI YAML parsing, and diff whitespace checks pass.
- Independent final review found one non-blocking but real OpenAPI typing gap:
  driver branches were runtime-exclusive but type-optional. Added a final DB4
  contract-hardening pass to emit a discriminated `oneOf` union before handoff.
- Closed the typing gap with a Huma `SchemaTransformer`: `DatabaseInput` is now
  a pure discriminator plus three closed named variants, each requiring only
  its matching nested object. Runtime decoding remains the same strict wire DTO.
- Regenerated OpenAPI and the Web client. Generated `DatabaseInput` is a true
  TypeScript union, and a compile-only regression now proves missing, mismatched,
  and multiple driver objects are rejected by the generated type.
- Final post-union gates pass: `go test ./... -count=1`, focused Setup/server race
  tests, `go vet ./...`, `go mod verify`, `pnpm check:web`, all 98 Web tests,
  production Web build, Playwright discovery (4 tests), Bash syntax, CI YAML
  parsing, and `git diff --check`.
- Contract regeneration is byte-deterministic at final hashes
  `26c7a0fe8c8c0a34045ada493c811a1d2a50d87ff480bfff8eb91890186f200c`
  (OpenAPI) and
  `cbdf230f168e7a6b6b8220c1c4a1ea2721dfaacc5fce3342812c9759ef742478`
  (generated TypeScript). DB4 is complete.

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
# 2026-08-10 — Access console final security hardening

- Serialized password login session creation with password reset, user disable/delete,
  user-role reductions, Administrator revocation, and role-grant reductions by locking
  the same password identity rows through session insertion or revocation.
- Added a forced interleaving regression proving an old-password login cannot leave an
  authenticatable session after a concurrent reset.
- Replaced the persisted unkeyed idempotency request digest with a domain-separated
  HMAC-SHA-256 keyed by the deployment session secret, preventing request digests for
  password operations from becoming offline password verifiers after a database leak.
- Backend focused tests are green; full Go, Web, generated-contract, and isolated
  production-browser gates remain in progress.
- Final full verification now passes `go test ./... -count=1`, `go vet ./...`,
  `pnpm check:web`, and `pnpm test:web` (12 files / 112 tests). The isolated
  Playwright production-server run and targeted race detector remain.
- Targeted `-race` coverage for auth/access and the production Next.js build now pass.
  The isolated Playwright runner could not bind localhost port 3301 in the managed
  sandbox (`EPERM`); the required escalation was rejected because the current Codex
  usage allowance is exhausted, so no browser-test assertion executed. E2E discovery
  remains green, but the real browser flow is explicitly unverified in this run.
- Removed the temporary workspace Playwright config. The sandbox likewise rejected
  deletion of its generated Go-cache directory at
  `/private/tmp/aginex-access-e2e.Dp5Wrg`; it contains only disposable compiler cache.
- Regenerated OpenAPI and the TypeScript client after the final backend changes. Both
  artifacts were byte-identical to the locked versions: OpenAPI SHA-256
  `bd4f07b4e3904aede55572734a443d368e4ef8eb3c2677e0f250fed070eee5d3`, client SHA-256
  `f702e467ce88d17d1add5aa53b9cb21c8aeeb8ab001f91880dd16dda7ebdd869`.
- Independent security review of the frozen candidate reports zero open P0/P1 issues.
  Final uncached repository tests, full vet, E2E discovery (4 tests), and diff whitespace
  checks pass after the last cross-role write-skew lock-order fix.
# 2026-08-10 — Storage provider profiles

- Started implementation from the approved multi-provider profile plan.
- Re-read the required planning, resource, schema, custom-operation, storage-upload, and admin-page skills.
- Session catch-up reported no unsynchronized context; recorded the dirty-worktree baseline before edits.
- Completed the baseline seam inventory. Chosen implementation keeps profile persistence in installation v2, exposes immutable runtime profile metadata through config, and adds exact routing behind an internal registry while preserving the public active store.
- Added strict installation configuration v2 with revision metadata and durable storage profiles while preserving strict v1 decoding.
- Added atomic profile updates with revision CAS, 0600 temporary files, fsync, symlink/permission checks, and a cross-process file lock.
- Setup and environment markers now persist the actual initial storage target instead of silently defaulting to local storage.
- Verified `go test ./internal/config ./cmd/server`.
- Added an immutable startup Storage Registry and wired API/worker stores by profile ID; retained profile construction failures are degraded while the active profile remains required.
- Added nullable indexed `storage_profile_id` migrations for all three dialects and both normal/legacy Files bundles, including safe current-schema re-adoption.
- New files bind the active profile; confirm/read/local content/idempotent replay resolve the file's own profile, and responses now include profile identity/name/preset.
- Added auditable, repeatable, unambiguous startup backfill for legacy file rows.
- Added cleanup payload v3 with profile/bucket identity and retained v1/v2 fail-closed routing; focused app, worker, migration, and cleanup tests pass.
- Added the complete permissioned `/api/v1/storage-settings` and `/api/v1/storage-profiles` surface with ETag/If-Match CAS, CSRF/idempotency, readiness tests, lifecycle constraints, environment-managed read-only mode, and compensating installation-file restore when auditing fails.
- Added provider-specific validation for Local, Alibaba OSS, AWS S3, MinIO, and Cloudflare R2, including production MinIO allowlists and metadata/link-local endpoint rejection.
- Added the responsive `/settings` Object Storage console, permission-aware navigation/actions, conditional provider fields, non-cached secret submission, restart/degraded notices, confirmation states, and profile names on the Files page.
- Full `go test ./...`, `pnpm check:web`, and all 112 Web unit tests passed before the final contract regeneration and release gates.
- Closed the file/audit concurrency window by holding the installation revision lock through the mandatory audit transaction and restoring the exact previous document before any later writer can proceed; rollback and concurrent-CAS tests pass.
- Added explicit CORS support for `If-Match` and exposed `ETag`; the real browser run caught this cross-origin contract gap before delivery and now has a dedicated preflight regression.
- Regenerated OpenAPI and the TypeScript client, then passed final `go test ./...`, `go vet ./...`, `pnpm check:web`, `pnpm test:web` (12 files / 112 tests), and the production Web build with `/settings` present.
- SQLite fresh/legacy adoption and the legacy `storage_profile_id` up/down path pass. PostgreSQL/MySQL matrix cases skip because `AGINEX_TEST_POSTGRES_DSN` and `AGINEX_TEST_MYSQL_DSN` are not configured.
- Local storage contract passes. Live S3-compatible and Alibaba OSS contracts skip because their endpoint/bucket/region/credential variables are not configured.
- Ran the full Playwright administrator workflow against an isolated SQLite installation and local storage root: all 4 tests passed, including first-run Setup, upload, Object Storage page, audit, access management, and responsive flows. Existing workspace dev servers were preserved.
- Final `git diff --check` passes; all storage-profile phases are complete.

# 2026-08-11 — General file uploads and resumable transfers

- Loaded the upload, admin-page, custom-operation, schema, verification,
  frontend-design, persistent-planning, and skill-creator guidance.
- Session catch-up reported no unsynchronized context. Recorded the substantial
  existing access/storage-profile dirty-worktree baseline and will preserve it.
- Added the explicit pre-release/current-baseline rule to `AGENTS.md` and updated
  the upload Skill from image-only v1 guidance to safe general/resumable files.
- Started Phase 1 contract and ownership inventory before parallel backend,
  storage/migration, and Web implementation.
- Split implementation into disjoint storage/provider, config/schema, Web, and
  HTTP/state-machine workstreams; generated contracts remain owned by the final
  integration pass.
- Added typed upload-policy, discriminated intent, file preview, session, part
  sign/ACK, and resume contracts without touching generated artifacts.
- Extended revisioned Storage Settings with runtime/pending upload policy and
  an audited, compensating `PUT /storage-settings/file-upload-policy`; focused
  app/config compilation passes with the task-scoped Go cache.
- Added actor-scoped effective upload-policy discovery and route-aware request
  limits: JSON APIs retain their configured cap while only Local binary PUT
  routes receive the 1 GiB absolute ceiling before exact handler validation.
- Completed the current-only installation v3 policy and unique Files migration
  baseline; no v1/v2 or current/legacy adoption branches remain.
- Completed generic streaming verification, safe controlled reads, Local/S3/OSS
  multipart capabilities, persisted resumable state, expiry/cancel cleanup jobs,
  and the bounded API-process repair scanner.
- Connected expiry and cancellation job enqueueing to the audited session
  transactions; provider identifiers and ETags stay out of jobs and responses.
- Regenerated OpenAPI and the TypeScript client once after the upload/session
  contracts converged. Final Web corrections, HTTP regressions, documentation,
  and repository-wide release gates are in progress.
- Completed the transfer workbench and policy settings UI, including 20-file
  review, four global transfers, two parts per file, XHR byte progress,
  retry/pause/resume/reselect recovery, safe preview/download, and EN/ZH copy.
- Closed adversarial upload-integrity findings: cloud and Local single uploads
  are create-only; Local publication is fsynced and atomic; multipart Local
  completion validates each open descriptor; browser UploadPart ETags remain
  the persisted completion manifest; cancel/delete cannot race completion.
- Hardened preview verification with full image decode and PDF xref/root
  structure checks, fixed single-intent authorization expiry and cleanup
  windows, request-context transfer deadlines, silent recovery without request
  dumps, and bounded Local orphan-staging maintenance.
- Focused storage, cleanup, configuration, HTTP, migration, and Web regressions
  pass. Stuck completing/verifying recovery and final repository/provider/E2E
  gates are the remaining Phase 5 work.
- Completed audited recovery for stale `completing`/`verifying` sessions with
  CAS leases, provider reconciliation, uncertainty-safe completion, and full
  streaming verification; recovery never aborts a potentially completed object.
- Independent security review closed all P0/P1 findings, including signed-URL
  overwrite races, cancel/complete and delete/complete races, exact browser ETag
  persistence, request-secret logging, Local publication durability, and stale
  single-intent authorization windows.
- The isolated SQLite Chromium run exposed concurrent confirm write-lock
  failures. SQLite connections now use WAL, a 10-second busy timeout, and
  immediate write transactions; the real five-file concurrent workflow passes
  without transient 500 responses.
- Added browser-level 33 MiB+ recovery acceptance: preserve only the ACKed first
  part, reload, reselect by fingerprint, sign/re-upload only the missing part,
  and verify the reassembled SHA-256.
- Final gates pass: `go test ./... -count=1`, `go vet ./...`, deterministic
  OpenAPI/client generation, `pnpm check:web`, 132 Web tests, production Web
  build, four Chromium E2E workflows, Agent Skill validation, and
  `git diff --check`.
- PostgreSQL/MySQL migration and live S3-compatible/OSS contract subtests remain
  explicitly skipped locally because their DSNs/endpoints/credentials are not
  configured; CI retains PostgreSQL 18, MySQL 8.4, required MinIO, and the
  secret-gated OSS job.

# 2026-09-06 — Alibaba OSS browser upload repair

- Read the required upload, debug, and file-planning Skills.
- Preserved the large in-progress backend-to-server refactor and unrelated
  dirty worktree.
- Ran focused existing storage/config/app tests successfully.
- Ran the secret-gated live OSS contract: presigned PUT, HEAD, and server read
  succeeded; signed GET failed with the provider's unsupported content-type
  override.
- Ran a bounded live browser-shaped probe: OPTIONS failed because bucket CORS
  is disabled; test objects were deleted and the temporary probe was removed.
- Started regression-test phase; no production code changed yet.
- Locked the implementation seams: OPTIONS-based OSS readiness using configured
  web origins, direct signed attachment reads without response-content-type,
  and authenticated server streaming for verified OSS previews.
- Added red tests for the OSS signed-read query, browser CORS preflight, and
  actionable XHR status-zero guidance. The frontend assertion failed as
  expected; the first Go attempt was blocked by the user build cache and will
  use the existing task-scoped cache pattern.
- Implemented the OSS preflight checker, actionable readiness problem, removed
  the unsupported response-content-type query, and added localized browser
  CORS/network guidance. The focused frontend test now passes.
- Added a red application regression proving verified OSS previews must use an
  authenticated provider-content route; implemented that route while retaining
  direct signed URLs for attachment downloads.
- Formatted the four edited admin files with the repository's Biome setup.
- Focused storage/application Go tests and the 10-test Files page suite pass
  after implementation.
- Regenerated OpenAPI and the typed admin client, then synchronized the
  canonical source allowlist into CLI scaffold templates and snapshots.
- Full CLI tests and the admin typecheck/lint gate pass. The full server suite
  remains in progress with all completed packages passing so far.
- The full server suite completed successfully; all 133 admin tests pass and
  the scaffold drift check reports a clean synchronized snapshot.
- The optimized Next.js administration build completes successfully.
- Read back the live bucket CORS contract with approved network access. Its
  existing rule already satisfies Aginex direct-upload requirements, so no
  destructive or redundant PutBucketCors operation was performed.
- The live end-to-end repair probe passes browser preflight, signed PUT, signed
  GET, and cleanup. The temporary probe object and local helper were removed.
- Final source review confirms persisted file provider values use the normalized
  `oss` driver, matching the authenticated preview routing guard.
- Re-synchronized after the final test/helper edits. The read-only template
  check and `git diff --check` now pass, and no temporary helper path remains in
  the scaffold manifest or snapshot.
- Gave control-plane and browser-readiness probes independent three-second
  budgets, reran focused storage/application regressions successfully, and
  synchronized that final source adjustment into the scaffold.
- The final CLI suite and template drift gate pass; the repeated full server
  suite is still running with every completed package passing.
- The repeated full server suite completed successfully after the independent
  readiness timeout adjustment.
