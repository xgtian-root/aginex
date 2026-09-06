# Aginex v1 Implementation Plan

## Current Goal: OSS Provider Read URLs and Durable File Cleanup (2026-09-06)

Implement provider-hosted signed OSS preview/download URLs, verified object
presentation metadata, strict OSS access-domain configuration, durable
PostgreSQL deletion jobs, automatic development worker orchestration, and a
re-runnable reconciliation path for existing ready OSS files.

### OSS Repair Phases

| Phase | Status | Exit criteria |
|---|---|---|
| O1. Regression contracts and configuration boundary | complete | Tests lock access-domain validation, provider-hosted reads, metadata finalization, and worker orchestration |
| O2. Storage and API implementation | complete | OSS uses the configured read host, verified MIME is finalized, and the API proxy route is retired |
| O3. Admin, CLI, and reconciliation | complete | Settings expose the field, dev launches configured workers, and existing objects can be reconciled with audit |
| O4. Runtime rollout and live checks | external_config_required | Current config is updated and services run with PostgreSQL jobs; inline preview awaits an OSS CNAME and destructive delete awaits an authenticated user session |
| O5. Full verification and template convergence | complete | Go/Web/build/provider checks pass and scaffold snapshots have no drift |

### OSS Repair Guardrails

- Preserve direct browser cloud uploads and verify bytes before promoting any
  object MIME; never inline active or unverified content.
- Keep service/control endpoints separate from the browser-visible read
  origin, and sign private OSS reads on the configured HTTPS origin.
- Keep cleanup asynchronous and durable; API acceptance is not completion, so
  a separately supervised worker must consume every accepted delete.
- Keep installation configuration strict at version 1 and update the current
  unpublished baseline without legacy readers or fallback fields.
- Preserve unrelated work in the already-dirty backend-to-server rename.

### OSS Repair Errors Encountered

| Error | Attempt | Resolution |
|---|---|---|
| Focused Go tests could not read the user Go build cache in the sandbox | 1 | Re-run with a task-scoped writable `GOCACHE`; do not change cache ownership or source behavior |
| Focused app tests retained the retired OSS API-proxy expectation and an OSS profile fixture lacked the new required origin | 1 | Replace the workaround regression with provider-hosted signing coverage and update strict OSS fixtures |
| Combined CLI/template/documentation patch assumed README wording from the generated template | 1 | Apply the source/CLI edits independently, then inspect and patch the repository README's actual worker section |
| Reconciliation test's minimal audit table used DTO field names instead of GORM column names | 1 | Align the test-only schema with `sanitized_before` and `sanitized_after`; production migrations are unchanged |
| Combined docs patch used the pre-stable guide's worker wording against Operations | 1 | Patch README/template and each Operations section independently from exact local context |
| OSS editor accessibility test expected only the visible label, but the nested hint is correctly part of the accessible name | 1 | Match the accessible name by its stable label prefix while retaining the descriptive hint |
| Template sync rejected a direct `.env.example` snapshot edit | 1 | Move the access-origin change to the canonical root `.env.example`, restore the snapshot copy, then let the sync tool regenerate it |
| Live reconciliation could not connect to local PostgreSQL inside the sandbox | 1 | Re-run the same scoped maintenance command with approved local database and OSS network access |
| Safari address-bar automation appended instead of replacing the stale proxy URL, and undocumented key names could not select/submit it reliably | 1 | Stop mutating the browser tab; verify the configured signed provider URL with an opt-in live storage contract that never prints the signed URL or credentials |
| Live OSS read used the configured provider host, verified MIME, empty durable disposition, and signed `inline` override but returned `attachment` with `x-oss-force-download: true` | 2 | A read-only Bucket CNAME query confirmed no domain is bound. Preserve signed provider URLs, document the provider constraint, and require a bound custom CNAME to complete live inline-preview rollout |
| Full admin suite retained the old official-domain placeholder after the UI began recommending CNAME for inline previews | 1 | Update the assertion to the custom-domain example and rerun the complete suite |
| The current bootstrap administrator variables are empty, so the live API cannot be authenticated for a destructive delete check | 1 | Do not bypass authentication or mutate the database directly; rely on the passing API/queue/handler contracts and leave the confirmed test object intact for user verification |
| Template drift check was invoked from the `server` working directory and could not resolve `cli/templates/sources.json` | 1 | Re-run the same check from the repository root, where the sync manifest paths are defined |

## Current Goal: `aginex new` Project Initialization (2026-09-04)

Implement a collision-safe project initializer with two explicit target modes:
`aginex new` initializes the current directory, while
`aginex new <name>` creates and initializes `<name>` below the current
directory. Generated projects must have one coherent Aginex application
composition, must not silently overwrite existing content, and must be covered
by CLI and generated-project verification.

### New Command Phases

| Phase | Status | Exit criteria |
|---|---|---|
| N1. Contract and template-boundary audit | complete | CLI conventions, public consumer APIs, required starter files, collision rules, and platform-safe paths are mapped |
| N2. Initializer implementation | complete | Both target modes render a deterministic dependency-based project without partial output or overwrites, using a public Aginex host boundary |
| N3. CLI and scaffold regression tests | complete | Argument validation, current/new-directory behavior, collisions, names, and generated artifacts are tested |
| N4. Documentation and Skill convergence | complete | README, CLI help, roadmap/status, and project-creation Skill describe the implemented behavior accurately |
| N5. Completion verification | complete | Focused Go tests, generated-project checks, formatting, and the applicable repository gates pass |

### Phase N1: Contract and template-boundary audit

- **Status:** complete

### Phase N2: Initializer implementation

- **Status:** complete

### Phase N3: CLI and scaffold regression tests

- **Status:** complete

### Phase N4: Documentation and Skill convergence

- **Status:** complete

### Phase N5: Completion verification

- **Status:** complete

### `aginex new` Guardrails

- Zero positional arguments target the current working directory; one argument
  targets a new direct child directory; more than one argument is rejected.
- Never overwrite a pre-existing file or initialize a non-empty target.
- Validate project names before creating the target and reject absolute paths,
  traversal, separators, dot entries, and ambiguous platform names.
- Render through a staging directory and publish only a complete scaffold so a
  failed render does not leave a partially initialized project.
- Keep generated project source dependent on public Aginex contracts; do not
  expose or import Aginex `internal` packages.
- Do not generate `.env`, credentials, database files, uploaded objects, or a
  committed administrator password.

### New Command Errors Encountered

| Error | Attempt | Resolution |
|---|---|---|
| First external consumer build lacked transitive checksums and a tidy module graph | 1 | Bundle the matching framework sums and derive an application-view indirect dependency graph that passes readonly build and `go mod tidy -diff` |
| A combined smoke command was run from the generated-project parent instead of the repository/project directory | 2 | Split repository build, generation, and consumer verification into commands with explicit working directories |
| Source-built CLI metadata exposed a non-downloadable `+dirty` pseudo-version | 1 | Reject dirty build metadata and fall back to the declared pre-release build version; release binaries still use their exact clean module/build version |
| Windows cross-compilation could not create a dependency-cache lock inside the sandbox | 1 | Re-run the unchanged read-only cross-build with approved Go module-cache access |
| Root `go mod tidy -diff` also reports an unrelated pre-existing `go-sqlite` directness change | 1 | Preserve the user's unrelated dependency work; verify the generated project independently with its own zero-diff tidy gate |
| The generated-project offline `pnpm install` lacked one cached package tarball | 1 | Re-run the same frozen-lockfile installation with approved network access, then execute all generated Web checks |
| The first global-CLI `check` probe inherited an unwritable user Go build cache | 1 | Re-run with a task-scoped `GOCACHE`; the complete generated-project check passed |
| A `go doc` probe attempted to resolve unrelated uncached modules through the sandboxed network | 1 | Inspect the already-cached `x/mod` source directly and keep all completion gates offline/task-cache scoped |
| A manual generated-project tidy probe inherited the unwritable user Go cache and emitted secondary package-scan noise | 1 | Re-run unchanged with a task-scoped `GOCACHE`; `go mod tidy -diff` completed with zero output |

## Current Goal: PostgreSQL Reinitialization Ownership Repair (2026-08-11)

Make `dev reinitialize` work when the configured local PostgreSQL role owns the
application objects but does not own the shared `public` schema. Preserve the
recoverable backup boundary and prove the fix without committing a reset to the
operator's live database.

### Repair Phases

| Phase | Status | Exit criteria |
|---|---|---|
| PR1. Failure and rollback audit | complete | The reported ownership failure is classified and both configuration/database rollback are verified by control flow |
| PR2. PostgreSQL regression contract | complete | Tests lock preflight ownership checks and transactional object-level archive behavior |
| PR3. Implementation and documentation | complete | The command creates a private backup schema and moves supported owned objects without renaming `public` |
| PR4. Non-persistent live verification | complete | The exact configured database passes an archive probe inside an explicitly rolled-back transaction |
| PR5. Relevant verification gates | complete | Focused CLI tests, full CLI/config tests, formatting, and documentation checks pass |

### Phase PR1: Failure and rollback audit

- **Status:** complete

### Phase PR2: PostgreSQL regression contract

- **Status:** complete

### Phase PR3: Implementation and documentation

- **Status:** complete

### Phase PR4: Non-persistent live verification

- **Status:** complete

### Phase PR5: Relevant verification gates

- **Status:** complete

### PostgreSQL Repair Guardrails

- Do not require ownership of the shared `public` schema and do not change its
  owner or privileges.
- Preflight every supported application object before the first DDL statement;
  reject foreign-owned or unsupported standalone objects fail-closed.
- Move objects and create the backup schema in one transaction so any error
  restores the original database layout automatically.
- Never print the installation DSN, password, provider credentials, or private
  configuration contents.
- Verify the live target only in a transaction that is always rolled back; the
  operator remains responsible for rerunning the confirmed real reset.

## Current Goal: Pre-release v1 Configuration and Safe Local Reinitialization (2026-08-11)

Withdraw installation v1/v2/v3 compatibility, keep the unpublished
installation document version fixed at v1, retain the single current database
migration baseline, and provide an explicit recoverable local-development
reinitialization command for disposing of stale pre-release state.

### Reinitialization Phases

| Phase | Status | Exit criteria |
|---|---|---|
| R1. Existing CLI/config/reset seam audit | complete | Current dev command, installation writer/reader, database targeting, backup primitives, and safety boundaries are mapped |
| R2. Regression contract | complete | Tests lock v1-only strict configuration and require explicit, local-only, recoverable reset behavior |
| R3. Implementation | complete | Configuration is v1-only and the reset command archives state before changing any local target |
| R4. Documentation and startup verification | complete | Agent/operations docs describe the pre-release v1 rule and reset workflow; stale-state and fresh-start behavior are exercised |
| R5. Full verification | complete | Go, Web, build, Skill, doctor/check, formatting, and relevant CLI integration gates pass |

### Phase R1: Existing CLI/config/reset seam audit

- **Status:** complete

### Phase R2: Regression contract

- **Status:** complete

### Phase R3: Implementation

- **Status:** complete

### Phase R4: Documentation and startup verification

- **Status:** complete

### Phase R5: Full verification

- **Status:** complete

### Reinitialization Guardrails

- Never reset automatically during `dev` startup; the operator must invoke the
  reset subcommand and acknowledge the exact target.
- Restrict destructive database handling to demonstrably local development
  targets; fail closed for production, environment-managed, remote, ambiguous,
  symlinked, or overly permissive configuration.
- Produce a mode-0600 recoverable backup before changing installation or
  database state, and never print secrets, DSNs, or provider credentials.
- Keep installation JSON strict and fixed at version 1 until the user declares
  a published compatibility boundary. Unknown fields and any other version fail.
- Do not restore legacy database migration families; reinitialization consumes
  stale pre-release state and starts from the sole current baseline.

### Errors Encountered

| Error | Attempt | Resolution |
|---|---|---|
| Sandbox denied the user Go build cache during the first focused config test | 1 | Re-run the same deterministic test with the already approved `go test` cache access rather than changing source or cache ownership |
| SQLite archive refactor retained the old three-result helper assignment | 1 | Read the exact compile line and align it with the new two-result helper signature |
| Combined documentation patch missed a wrapped Operations sentence | 1 | Verified the patch was atomic/no-op, then split documentation updates by file and exact local context |
| Operations-only patch still combined two contexts and missed the second wrap | 2 | Stop combining contexts: apply the installation/reset section first, then inspect and patch the rollback sentence independently |
| A double-quoted search pattern contained Markdown backticks and invoked an empty shell substitution | 1 | No data was exposed; use single-quoted patterns for all subsequent searches containing backticks |
| CLI help probe was blocked by the sandboxed user Go build cache | 1 | Re-run the read-only help command with approved Go cache access; no source or environment changes are needed |
| Planning completion checker found only table-based phases and reported 0/0 | 1 | Added the Skill's canonical `### Phase` plus `**Status:** complete` markers without discarding the detailed phase table |
| Final help-copy patch assumed aligned Cobra field spacing | 1 | Apply against the exact gofmt output and keep the documentation context separate |
| PostgreSQL archive attempted to rename `public`, but the configured application role does not own that shared schema | 1 | Replace schema rename with a preflighted transactional move of application-owned objects into a private backup schema |
| Focused CLI regression could not read the user Go build cache inside the workspace sandbox | 1 | Re-run the unchanged focused test with the already approved `go test` cache access; do not alter cache ownership or source |
| Rollback-only PostgreSQL probe found the application-owned audit immutability trigger function in `public` | 1 | Expand the archive inventory to supported functions with explicit owner checks and dependency-safe moves; keep unknown standalone objects fail-closed |
| Combined planning-file patch used context from the wrong file and applied nothing | 1 | Split the task-plan error entry and findings/progress updates into exact per-file patches |

## Current Goal: People and Role Management Console (2026-08-10)

Complete the existing read-only access administration surface with secure
administrator-managed local users, custom roles, user-role assignments, and
role-permission grants. Keep registered permission definitions code-owned,
keep the built-in Administrator role immutable and synchronized to every
registered permission with `all` scope, and preserve atomic audit-backed writes.

### Access Management Phases

| Phase | Status | Exit criteria |
|---|---|---|
| A1. Contract and invariant audit | complete | Existing identity, role, permission, session, audit, API, UI, and test seams are mapped and public operations are locked |
| A2. Backend management operations | complete | Typed user/role CRUD and assignment operations enforce RBAC, lifecycle invariants, atomic audit, and RFC problem responses |
| A3. Permission-aware console | complete | Responsive People and Access pages expose only authorized actions with complete form, empty, error, and destructive states |
| A4. Contract generation and focused tests | complete | OpenAPI/client are regenerated and success/401/403/invariant/audit paths pass focused Go and Web tests |
| A5. Full verification and review | complete | Go, Web, production build, drift, and independent security review pass; real browser execution is documented as environment-blocked |

### Access Management Guardrails

- Permission definitions remain module-declared and startup-synchronized; the
  console edits role grants, never arbitrary permission codes.
- `Administrator` is reserved, cannot be renamed, weakened, or deleted, and
  always receives every registered permission with `all` scope.
- Every successful mutation commits its business state and audit event in one
  transaction; failed authorization or validation changes nothing.
- Public registration remains disabled. Only authorized administrators may
  create local users and assign roles.
- Prevent self-lockout and loss of the last active Administrator through user
  disable/delete or role reassignment.
- Preserve all existing application-owned files and use the repository's
  generated OpenAPI/TypeScript path instead of editing generated artifacts.

### Locked Access Contract

- Permission catalog: `permissions:read`; runtime permission definitions remain
  immutable and expose their valid `own|all` grant scopes.
- People permissions: `users:create|read|update|delete|assign-roles|enable|disable|reset-password|grant-administrator|revoke-administrator`.
- Role permissions: `roles:create|read|update|delete|grant`.
- User CRUD lives at `/api/v1/users[/{id}]`; role replacement, enable/disable,
  password reset, and Administrator grant/revoke are explicit subresource/action
  operations. Email is immutable after local identity creation.
- Role CRUD lives at `/api/v1/roles[/{id}]`; grants are atomically replaced at
  `/api/v1/roles/{id}/grants`. Permission codes, not database permission IDs,
  are the stable request identity.
- User and role create requests may include initial assignments only when the
  actor also holds the corresponding assignment/grant permission; the service
  applies the same delegation ceiling as later replacement operations.
- Generic user-role assignment excludes Administrator. Its grant/revoke uses
  explicit operations, requires an existing Administrator actor, and serializes
  last-active-Administrator checks.
- Disabling, deleting, or resetting a user revokes that user's browser sessions
  in the same audited transaction. User deletion is soft deletion; its email
  and identity remain reserved.
- Custom roles with assigned non-deleted users cannot be deleted. Administrator
  role metadata/grants/deletion remain entirely bootstrap-owned.

## Current Goal: Remove Administrator Password Length Limits (2026-08-10)

Remove administrator password minimum/maximum length constraints across Setup
and environment bootstrap creation, Argon2 hashing/verification, login,
OpenAPI, generated clients, UI validation/copy, and tests. Retain non-length
safety boundaries such as non-empty input, bounded HTTP requests, secret
handling, hashing parameters, rate limits, and generic error responses.

### Administrator Password Limit Phases

| Phase | Status | Exit criteria |
|---|---|---|
| AP1. Constraint inventory | complete | Every backend, contract, UI, copy, automation, and test length assumption is mapped |
| AP2. Runtime and public contract | complete | Creation, hashing, verification, and login accept non-empty administrator passwords without explicit min/max checks |
| AP3. UI and regression coverage | complete | Browser validation/copy match the new contract and boundary regressions cover short and long values |
| AP4. Generation and full verification | complete | Generated artifacts are deterministic and complete Go/Web/build gates pass |

### Administrator Password Guardrails

- Remove length policy only for administrator credentials; database
  credentials, session secrets, tokens, and unrelated field limits stay
  unchanged.
- Do not expose password values in validation errors, logs, responses, test
  failure output, planning files, or generated documentation.
- Keep the global request-size ceiling as the physical upper bound and preserve
  the exact password bytes through hashing and bootstrap.

## Current Goal: Local PostgreSQL and MariaDB Verification (2026-08-10)

Provision the operator-requested local PostgreSQL role/database and MariaDB
database, then prove the new structured Setup contract against both live
servers. Retain the created database objects and do not reset unrelated data.

### Local Database Verification Phases

| Phase | Status | Exit criteria |
|---|---|---|
| L1. Service and object preparation | complete | Local listeners are identified and requested role/database objects exist |
| L2. Direct credential verification | complete | Each requested credential can select its retained `aginex` database |
| L3. Structured Setup verification | complete | Both driver-specific `/setup/database/test` requests return 200 against live servers |
| L4. Full initialization and retention audit | complete | Both dialects reach application readiness and final objects remain present after temporary processes stop |

### Local Verification Guardrails

- Never log or commit the supplied database passwords or assembled DSNs.
- Create or update only the explicitly requested local role/database objects;
  do not delete databases, roles, or unrelated data.
- Distinguish direct CLI connectivity, structured Setup connectivity, and full
  application initialization in the final evidence.

## Current Goal: Structured Setup Database Configuration (2026-08-10)

Update the existing first-run Setup database step so SQLite accepts a storage
directory and database name separately, while PostgreSQL and MySQL accept
explicit connection fields and rely on the backend to construct the runtime
DSN. Preserve verification, initialization, fail-closed behavior, localization,
and generated-contract determinism.

### Structured Database Configuration Phases

| Phase | Status | Exit criteria |
|---|---|---|
| DB1. Existing contract and flow audit | complete | Setup request types, DSN parsing/validation, UI state, tests, and delivery constraints are mapped |
| DB2. Backend contract and DSN assembly | complete | Typed per-driver inputs are validated and safely assembled into the existing runtime configuration |
| DB3. Setup UI and localization | complete | Driver-specific labeled fields, SQLite directory/name inputs, and responsive states use the new contract |
| DB4. Regression and contract verification | complete | Focused Go/Web tests, generated artifacts, full checks, and build pass without unrelated drift |

### Structured Database Configuration Guardrails

- Keep the high-authority assembled DSN backend-owned; never accept a raw DSN
  from the browser for PostgreSQL or MySQL.
- Treat SQLite directory and database name as separate untrusted inputs; prevent
  traversal or ambiguous filenames while retaining the existing path-safety
  boundary.
- Keep secrets out of API responses, logs, audit metadata, and user-facing
  errors.
- Preserve all unrelated worktree changes and update OpenAPI/generated clients
  through the repository-owned generation path.

### Locked Structured Input Contract

- `database.driver` remains `sqlite`, `postgres`, or `mysql`, and exactly one
  matching nested object is accepted.
- SQLite: `sqlite.directory`, `sqlite.filename`.
- PostgreSQL: `postgres.host`, `port`, `database`, `username`, `password`, and
  `sslMode` (`disable`, `require`, `verify-ca`, `verify-full`).
- MySQL: `mysql.host`, `port`, `database`, `username`, `password`, and `tlsMode`
  (`disabled`, `required`, `skip-verify`).
- Legacy browser `dsn` payloads are rejected by strict decoding. Environment
  `AGINEX_DATABASE_DSN` and managed installation v1 remain supported unchanged.

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
| Combined race orchestration returned a background session after four packages without preserving the final `internal/app` exit in its summarized output | First full focused race pass | Re-run `internal/app` race alone, retain its session ID, and poll to the explicit successful completion at 69.719s |
| Final audit found an environment-bootstrap password could exceed the configured login request-body ceiling | Post-focused P3 review | Encode the exact login JSON during config validation and require operators to raise `AGINEX_HTTP_MAX_BODY_BYTES`; this preserves unbounded password policy while preventing an unusable credential |
| Sandboxed CLI probes could see the local database listeners but TCP connects to ports 5432/3306 returned `Operation not permitted` | First direct credential probes | Repeat only the scoped localhost database commands with network approval; both credentials then connected successfully |
| The isolated Setup API reached its listen log and immediately stopped with a redacted bind error in the sandbox | First live structured-Setup attempt | Re-run the same isolated loopback server with scoped bind approval, then exercise both database-test requests and stop the process |
| Interrupted OpenAPI-union work had already saved a `DatabaseInput.TransformSchema` while the integration pass added a second copy | First focused compile after union hardening | Keep the documentation-only implementation in `internal/setup/openapi.go`, remove the duplicate contract-layer method/types, format, and rerun the Setup package |
| Strict generated union removed direct `DatabaseInput["postgres"]` / `["mysql"]` indexing | First post-union Web typecheck | Derive transport mode types with `Extract<DatabaseInput, {driver: ...}>`; the Wizard now consumes the discriminant explicitly |
| Initial negative TypeScript contract assertions placed `@ts-expect-error` above the call while errors were attached to nested properties | First generated-client type assertion check | Move each directive immediately above the invalid property; `pnpm check:web` then proves the expected errors remain active |
| Isolated production Web preview used the already-built client bundle, so the runtime-only `NEXT_PUBLIC_API_URL` did not replace its same-origin probe target | 1 | Stop that preview and try a dev compile with the isolated API URL; production build correctness remains covered by the successful build gate |
| The isolated Next dev preview initially reported ready but then detected the user's existing workspace dev lock on port 3000 and exited | 1 | Preserve the user's running process and do not kill or alter it; rely on isolated API plus automated Web gates rather than disturb active work |
| In-app browser local-page refresh was blocked by the browser URL safety policy after preview recovery | 1 | End browser control and close the QA tab without switching to another browser surface; report actual visual QA as unavailable rather than circumventing the policy |
| In-app browser backend did not support the documented `networkidle` wait state | 1 | Attempt a supported DOM-content wait once; the subsequent local URL policy block ended browser QA before the fallback could run |
| First combined focused gate found `cmd/server/runtime_test.go` still POSTed the old internal raw-DSN request and received the intended 400 | 1 | Update the process-level HTTP regression to marshal public `SetupCompleteInput` with SQLite directory/filename, then rerun focused server tests |
| First post-generation Web typecheck referenced the removed `SetupCompleteRequest` schema alias | 1 | Point the public Web alias at generated `SetupCompleteInput`; all driver-specific generated request types were already correct |
| First contract generation used the sandbox-disallowed user Go build cache | 1 | Re-run the same repository generator with task-scoped `GOCACHE=/private/tmp/aginex-go-cache`; no generated file was written before the failure |
| A generation-command inventory ended with zsh `no matches found` for optional `Makefile*` | 1 | Avoid optional unquoted globs in zsh; inspect explicit files or use `rg --files` first. The preceding read-only file output was unaffected |
| A combined dependency/source search ended with zsh `no matches found` for an optional `docker-compose*.yml` glob | 1 | Quote or avoid optional shell globs and run repository searches with explicit existing paths; earlier read-only output remained valid |
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
| New discriminated upload response made the existing single-upload struct literals fail pointer assignment | First contract-only compile for general files | Update single responses and idempotency sanitization to use an optional signed request; resumable replay never regenerates a provider upload |
| Focused app/config compile inherited the sandbox-blocked macOS user Go build cache | First settings-policy compile | Re-run with a task-scoped `GOCACHE=/private/tmp/aginex-file-upload-go-cache`; source formatting completed before the cache failure |
| App compile overlapped the storage workstream between removing legacy Policy and updating adapter constructors | First upload-policy endpoint compile | Do not patch the parallel-owned storage files; retain the correctly registered route and rerun after the provider slice reaches a compiling handoff |
# 2026-08-10 — Storage provider profiles

## Goal

Implement permission-aware, restart-applied storage profiles for Local, Alibaba OSS, AWS S3, MinIO, and Cloudflare R2, with safe installation-file persistence, exact file/cleanup routing, generated contracts, and a responsive `/settings` console.

## Phases

| Phase | Status | Deliverable |
|---|---|---|
| 1. Baseline and conflict inventory | complete | Preserve current access-console work and lock implementation seams |
| 2. Installation v2 and storage registry | complete | Versioned profile persistence, atomic CAS, provider factories, immutable runtime registry |
| 3. Files schema and cleanup routing | complete | Three-dialect migrations, audited backfill, exact API/worker routing, cleanup v3 |
| 4. Profile API, permissions, and audit | complete | CRUD/test/activate/archive/restore, CAS, readiness, audit compensation |
| 5. Settings UI | complete | Permission-aware responsive provider-profile console and Files labels |
| 6. Verification and documentation | complete | Final contracts, Go/Web tests, build, migration/provider checks, docs |

## Locked decisions

- Profiles remain in the private installation file; file rows store stable profile IDs.
- Explicit `AGINEX_STORAGE_DRIVER` makes profile management read-only.
- All changes are staged for restart; no API/worker hot swap.
- Active storage readiness is required; retained profile failures are degraded.
- Preserve and merge all pre-existing dirty-worktree changes.

# 2026-08-11 — General file uploads and resumable transfers

## Goal

Replace the image-only Files surface with a safe multi-file transfer workspace,
configurable 1 MiB–1 GiB upload policy, progress/results, and optional 32 MiB
multipart resume across Local, S3-compatible, and OSS storage.

## Phases

| Phase | Status | Deliverable |
|---|---|---|
| 1. Baseline, Agent constraint, and contract | complete | Preserve current storage-profile/access work, record the pre-release rule, and lock current-only contracts |
| 2. Configuration and generic single upload | complete | Current installation policy, generic streaming verifier, safe read disposition, typed API |
| 3. Multipart storage and persistence | complete | Three-dialect schema, Local/S3/OSS multipart capability, audited state machine and cleanup |
| 4. File transfer and Settings UI | complete | Multi-file review queue, XHR progress/results/resume, responsive settings policy panel |
| 5. Contracts, tests, documentation, and release gate | complete | Generated artifacts, focused/full tests, provider/dialect evidence, docs and final review |

## Locked decisions

- Aginex is unpublished: do not retain compatibility branches for unshipped
  installation versions, APIs, generated clients, or migration layouts.
- The final baseline uses one current installation document with a file upload
  policy; default maximum is 10 MiB and resumable upload defaults off.
- Resumable mode applies only to files strictly larger than 32 MiB, uses fixed
  32 MiB parts, expires after 24 hours, and never blocks an already-created
  session when policy or active profile later changes.
- Arbitrary file types are accepted, but only verified JPEG/PNG/WebP/GIF/PDF
  may preview; every other file is forced to attachment.
- Preserve and merge every unrelated dirty-worktree change already present.

# 2026-09-06 — Alibaba OSS browser upload repair

## Goal

Make an activated Alibaba OSS profile genuinely usable from the browser: fail
readiness when direct-upload CORS is absent, keep upload errors actionable, and
make signed downloads compatible with OSS without weakening verified preview
security.

## Phases

| Phase | Status | Deliverable |
|---|---|---|
| 1. Reproduction and contract decisions | complete | Live PUT/read/preflight evidence and bounded provider-neutral design |
| 2. Regression tests | complete | Red tests for OSS CORS readiness, signed reads, and browser status-zero guidance |
| 3. Backend and frontend implementation | complete | Provider capability, secure read behavior, and actionable UI copy |
| 4. Canonical template synchronization | complete | Generated API artifacts and scaffold snapshots synchronized from canonical sources |
| 5. Verification | complete | Focused/full Go and Web gates, generated-contract/template drift, and live OSS preflight/upload/download/cleanup all pass |

## Locked decisions

- Do not make the bucket public or expose credentials; keep browser uploads on
  short-lived signed PUT requests.
- OSS profile readiness must distinguish control-plane bucket access from the
  CORS policy required by the browser data plane.
- Never use OSS `response-content-type`; attachments may remain signed direct
  reads, while verified inline previews must retain a server-controlled safe
  content type.
- Preserve all pre-existing refactor and planning-file changes.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---|---|
| Live OSS browser preflight returned `403 AccessForbidden` because CORS is disabled | Initial provider probe | Add validated CORS readiness and document/configure the required bucket rule |
| Live OSS signed read returned `400 InvalidRequest` for `response-content-type` | Existing cloud contract | Remove the unsupported OSS query parameter and preserve inline safety through a controlled application path |
| Focused Go test could not write the macOS user build cache | First red-test run | Re-run all Go checks with a task-scoped `GOCACHE` under `/private/tmp` |
| New frontend failure-path test reached a missing `ApiError` export in the existing full API mock | First red-test run | Add the minimal mock class so `localizeApiError` can evaluate the intended fallback path |
| New browser-readiness interface used `context.Context` without importing `context` | First implementation compile | Add the missing standard-library import and rerun the focused package |
| The admin workspace does not provide a `prettier` command | First formatting attempt | Use the repository's configured Biome formatter on only the edited admin files |
| OSS SDK `GetBucketCors` returned a non-service wrapper for the unconfigured bucket | First live readback | Inspect the unwrap type chain without emitting endpoints or credentials, then only write after independently confirming the browser preflight remains disabled |
| Sandboxed live CORS inspection failed DNS resolution | First inspection command | Re-ran the same bounded read-only helper with approved network access |
| Final scaffold drift check failed after later source/test updates | First final drift gate | Re-run canonical synchronization after all source changes, then repeat the read-only check |
