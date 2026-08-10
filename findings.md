# Findings

## Administrator Password Length Limits — 2026-08-10

- User direction: remove administrator password length restrictions. Scope is
  the first-run Setup administrator credential plus every downstream boundary
  needed to create and authenticate that same credential.
- The intended change removes explicit minimum and maximum length checks while
  keeping a non-empty credential, request-size bounds, secret redaction,
  throttling, and the existing password hashing boundary.
- Length assumptions exist in `AdministratorConfig`, Setup HTTP validation,
  Wizard button state/HTML attributes/copy, the Argon2 `Hash` and `Verify`
  helpers, the login request schema/binding and Login HTML input, and the
  production environment-bootstrap validator. Removing only the Setup form
  would create credentials that cannot be hashed or used to sign in.
- `CredentialLooksInsecure` is not written as a minimum length rule, but its
  eight-distinct-character test creates an effective minimum of eight. The
  administrator path must stop using that diversity heuristic if short values
  such as a six-character operator-selected password are to work. Session
  secrets must retain the stronger helper unchanged.
- The implementation will keep password non-emptiness and reject placeholder,
  repeated-template, or surrounding-whitespace values during administrator
  creation without enforcing diversity or byte count. Login must accept the
  exact stored value; the global HTTP body limit remains the physical bound.
- Setup requests are already capped at 64 KiB and normal application requests
  use the configured HTTP body ceiling (12 MiB by default). These provider-wide
  transport limits remain; no password-specific `minLength`, `maxLength`,
  `min`, `max`, `len <`, or `len >` policy should remain.
- Runtime implementation now accepts one-character, six-character, and values
  beyond the old 1024-byte maximum through Setup, production environment
  bootstrap, Argon2 Hash/Verify, and the real login handler. Empty values remain
  invalid, and user-password creation still rejects surrounding whitespace,
  known placeholders, and repeated templates without applying diversity.
- The infrastructure/session-secret helper retains its original diversity
  policy. Repeated-template detection was changed from quadratic prefix scans
  to a linear prefix-function algorithm so accepting longer administrator
  passwords does not amplify validation cost.
- Generated OpenAPI now describes both `AdministratorConfig.password` and
  `LoginRequest.password` as only `type: string` plus `writeOnly: true`; neither
  contains a minimum or maximum. Required-object fields and runtime non-empty
  validation remain separate from password length metadata.
- Environment bootstrap now performs a transport-consistency check: it encodes
  the actual future login JSON and rejects only when the configured global body
  ceiling is too small, directing the operator to increase
  `AGINEX_HTTP_MAX_BODY_BYTES`. This prevents creation of an unusable password
  without reintroducing a password-field maximum or exposing its value.
- Final contract output is deterministic. OpenAPI SHA-256 is
  `2fe71878a50f838b41a5e4403a4b099ad5dcfd3c2840fd9746b566a91411fcf0`;
  generated TypeScript remains
  `cbdf230f168e7a6b6b8220c1c4a1ea2721dfaacc5fce3342812c9759ef742478`
  because TypeScript's string type never encoded the removed JSON Schema
  length annotations.

## Local PostgreSQL and MariaDB Verification — 2026-08-10

- PostgreSQL 14.20 (Homebrew) listens on loopback port 5432. The requested
  `aginex` login role was absent before this task, was created as a non-superuser,
  and owns the newly created `aginex` database.
- MariaDB 12.0.2 listens on port 3306. The requested root credential connects
  over TCP, and database `aginex` exists with `utf8mb4` /
  `utf8mb4_unicode_ci` defaults.
- Direct authenticated selections reported `current_user=aginex` and
  `current_database=aginex` on PostgreSQL, and selected schema `aginex` under
  `root@localhost` on MariaDB.
- An isolated development-mode Aginex Setup API received the exact new public
  structured inputs. PostgreSQL used host/port/database/user plus
  `sslMode=disable`; MariaDB used the MySQL branch plus `tlsMode=disabled`.
  Both `/api/v1/setup/database/test` calls returned HTTP 200 with `status=ok`.
- Independent full Setup runs also completed migrations and bootstrap on both
  servers, reached application mode, and returned HTTP 200 readiness. Each
  retained database now has 17 base tables, applied Goose version 8, and one
  active integration administrator created by its respective run.
- PostgreSQL's migration matrix additionally passed `Up -> DownToZero -> Up`
  before the final complete Setup. The final database is left migrated, not
  empty. Local encrypted PostgreSQL/MySQL transport was not tested: the chosen
  local modes intentionally disabled SSL/TLS.
- All temporary Aginex test processes were stopped. The root task's isolated
  config path was never published, while both requested databases, the
  PostgreSQL role, schemas, migration state, and bootstrap data remain intact.

## Structured Setup Database Configuration — 2026-08-10

### User Direction

- SQLite Setup must collect a storage directory and database name separately.
- PostgreSQL and MySQL Setup must collect necessary connection fields rather
  than exposing a raw connection-string input.
- The backend owns final DSN construction for all structured inputs.

### Investigation Status

- Repository was clean on `main` before implementation.
- Existing Setup, generated contract, persistence, and regression boundaries
  are being audited before choosing the wire shape.

### Initial Backend Findings

- `internal/setup.DatabaseConfig` currently serves two incompatible roles: it
  is the browser request shape (`driver` plus write-only raw `dsn`) and the
  internal/persisted installation value passed into runtime configuration.
- Both database-test and completion handlers validate only the driver enum,
  trimmed non-empty DSN, and an 8192-byte limit before handing the value to the
  server initializer.
- The server initializer copies that value directly into `config.Database` for
  validation/opening and later persists the same DSN through the Setup
  installation boundary.
- The production composition deliberately rejects SQLite and MySQL when
  `Environment == production`, though development/test and derived
  compositions retain all three drivers. This pre-existing policy must remain
  explicit in UX/error behavior.

### Direction Under Evaluation

- Separate browser-facing structured input DTOs from the existing internal
  `DatabaseConfig`. Convert and validate once at the HTTP boundary so raw
  PostgreSQL/MySQL DSNs are no longer accepted while runtime persistence can
  continue storing the assembled high-authority DSN.
- Prefer a driver-discriminated nested request shape so strict JSON decoding can
  reject irrelevant/mixed driver fields and OpenAPI can describe each driver's
  password as write-only.

### Runtime and Persistence Details

- SQLite opening already calls `os.MkdirAll(filepath.Dir(dsn), 0750)` before
  handing the path to the SQLite dialector. A structured directory plus file
  name can therefore assemble to one cleaned path without changing the runtime
  database abstraction.
- PostgreSQL currently accepts whatever GORM's PostgreSQL dialector accepts;
  MySQL likewise passes the DSN to `go-sql-driver/mysql` through GORM with
  version discovery disabled. Correct escaping is essential when constructing
  credentials, database names, IPv6 hosts, and query options.
- Managed installation files intentionally persist the assembled DSN, while
  environment-owned installations persist only a driver marker. The public DTO
  change does not require an installation schema migration.
- `config.Validate` validates supported drivers and cross-provider invariants
  but does not itself validate or parse DSN contents. The Setup conversion
  boundary must enforce all structured-field invariants before opening.

### Preliminary Field Model

- SQLite: `directory` and `databaseName`; the latter must be a single filename,
  not a path. The directory may be absolute or a clean relative path chosen by
  the operator.
- PostgreSQL/MySQL: host, port, database name, username, password, plus a small
  explicit security/connection option set. Standard-library URL construction
  is suitable for PostgreSQL; the MySQL driver's own formatter is preferable
  to hand-written DSN escaping.

### Initial Frontend Findings

- `SetupWizard` owns one generated `DatabaseConfig` object with a raw `dsn`,
  resets it to a driver-specific connection-string example, and fingerprints
  normalized JSON so any edit invalidates the successful connection test.
- The database step currently renders one input. PostgreSQL/MySQL DSNs are
  masked wholesale with a visibility toggle; SQLite shows a plain path.
- The same normalized database object is submitted to test, completion, and
  retry paths, so a structured discriminated state can preserve the existing
  exact-configuration verification invariant with JSON fingerprinting.
- English and Simplified Chinese catalogs explicitly refer to “connection
  string”/“DSN” and will need outcome-oriented field labels, help text, and
  success copy. Current E2E selectors also branch between `Database path` and
  `Connection string`.
- No focused component test was found by the first scoped search; API safety
  tests and the serial Playwright flow directly construct/assert the old raw
  DSN shape.

### UX Direction

- Render two labeled SQLite controls (directory and database file name).
- Render a responsive host/port row plus database, username, password, and
  driver-specific transport-security controls for server databases. Keep only
  password masked, with its existing accessible reveal interaction.
- Preserve 44px controls, visible labels, field-level hints, test invalidation,
  and keyboard/responsive behavior from the completed Setup design.

### Chosen Conversion Boundary

- Keep `DatabaseConfig {Driver, DSN}` as an internal runtime/install value.
- Make the HTTP request schemas carry a new structured `DatabaseInput` and
  convert it to `DatabaseConfig` in `testDatabase`/`completeSetup` before the
  request crosses into the asynchronous Supervisor lifecycle.
- Introduce a distinct internal initialization request containing the assembled
  database config and administrator config. This keeps the detached request,
  test invocation, application initializer, and installation store free of the
  public password-bearing per-driver structures and preserves their existing
  exactly-once cleanup/zeroing behavior.
- The Supervisor currently clears `request.Database.DSN` and the administrator
  password after every detached attempt, then commits that same internal config
  after candidate readiness. Retaining this boundary avoids persisting or
  logging the structured input object.

### Server Option Direction

- PostgreSQL will use a URL DSN assembled with `url.UserPassword`,
  `net.JoinHostPort`, an escaped database path, and an allowlisted SSL mode.
- MySQL will use the already-direct `github.com/go-sql-driver/mysql` dependency
  and `Config.FormatDSN`, with TCP host/port, `parseTime=true`, and an explicit
  allowlisted TLS mode rather than accepting arbitrary query parameters.
- Default ports are frontend conveniences; the wire contract still requires an
  explicit integer in the 1–65535 range so the submitted configuration is
  unambiguous.

### Presentation and Verification Seams

- Existing Setup CSS already centralizes form layout, labels, inputs, secret
  controls, responsive breakpoints, high-contrast, forced-colors, and reduced
  motion. A small reusable two-column field grid can extend it without a new
  component system.
- The repository contract path is `pnpm generate:contracts`, which runs the Go
  OpenAPI generator, `openapi-typescript`, and Biome formatting. Web completion
  still requires `pnpm check:web`, `pnpm test:web`, and `pnpm build:web`.
- The serial Playwright flow receives a driver plus raw DSN from environment.
  It must parse that external test fixture into the same driver-specific form
  fields, because deployment environment configuration may remain DSN-based
  even though the browser Setup API no longer is.
- Schema tags already use Huma's `minimum`/`maximum`, `minLength`/`maxLength`,
  enum, and `writeOnly` metadata, so port bounds and secret classification can
  be expressed in generated OpenAPI.

### DB1 Audit Closure

- Focused baseline gates passed before implementation:
  `go test ./internal/setup ./cmd/server -count=1` and the existing Web suite
  (7 files / 93 tests).
- No database schema migration is involved. Existing managed installation v1,
  configured restart, worker startup, and environment DSN injection remain
  byte-for-byte compatible because only browser Setup wire input changes.
- Strict JSON decoding makes this intentionally breaking for old Setup clients:
  a legacy `dsn` field becomes an unknown field and returns the existing generic
  400 problem. Keeping a deprecated raw-DSN escape hatch would conflict with
  the requested backend-owned assembly boundary.
- The production PostgreSQL gate runs before database opening and stays intact;
  the UI continues explaining that SQLite/MySQL are for development, tests, and
  derived compositions.
- Browser Setup documentation, API safety tests, serial PostgreSQL E2E, SQLite
  image smoke, OpenAPI assertions, and generated clients all directly depend
  on the old shape and are included in DB4.

### Locked Wire Fields

- Top level: `database.driver` plus exactly one of `sqlite`, `postgres`, or
  `mysql`.
- SQLite nested fields: `directory`, `filename`.
- PostgreSQL: `host`, `port`, `database`, `username`, `password`, `sslMode` with
  `disable|require|verify-ca|verify-full`.
- MySQL: the same five connection fields plus `tlsMode` with
  `disabled|required|skip-verify`; the assembled DSN always enables
  `parseTime=true`.

### Automation and Documentation Implementation Notes

- The real browser workflow now consumes structured E2E environment fields and
  fills the same visible per-driver labels as an operator. CI supplies explicit
  PostgreSQL host `127.0.0.1`, port `5432`, database/user/password `aginex`, and
  `sslMode=disable` for its local service.
- SQLite image smoke now posts `directory=/data` and
  `filename=aginex.db` for both test and completion, retaining its configured
  restart and permanent Setup-closure assertions.
- Documentation now distinguishes browser structured input from environment
  `AGINEX_DATABASE_DSN`: Setup assembles and persists a managed DSN, while
  preconfigured deployments and workers retain the existing environment-owned
  connection-string contract.
- SQLite directory copy explicitly identifies it as a backend host/container
  path, avoiding the unsafe implication that a browser directory picker can
  grant access to the server filesystem.

### Implementation Review Findings

- Backend conversion now has one `resolveDatabaseInput` dispatch that requires
  exactly one driver-matching nested object. PostgreSQL uses escaped URL
  user-info plus `net.JoinHostPort`; MySQL uses the canonical formatter and
  round-trips the DSN through `ParseDSN` to prove delimiter-bearing credentials
  still resolve to the submitted endpoint and identity.
- SQLite validation keeps filenames to one portable basename and rejects
  parent segments, URI/query syntax, volume names, separators, and control
  characters before joining the selected backend directory.
- Review found database passwords were initially treated as optional in both
  schema and UI. Because these are part of the requested necessary server
  fields, backend and frontend owners were asked to require 1–1024 bytes while
  continuing to preserve meaningful surrounding spaces and never trim secrets.
- Review also asked the backend owner to make an explicit tested decision on
  natural trailing directory separators (`/data/`) versus canonical-only
  input, without weakening the `..` traversal rejection.
- Frontend state is now a discriminated driver draft with string ports for
  usable empty/edit states, normalized non-secret text, and exactly one nested
  API object. A simple verified flag is reset on every edit, eliminating the
  previous duplicate password-bearing JSON fingerprint string.
- The new form is single-column by default and becomes a two-column grid at
  36rem; host/port and SQLite directory/filename receive purpose-specific
  proportions within the existing 29rem+ desktop form boundary.

### Final Independent Review

- No P0/P1 issue, credential response leak, stale browser raw-DSN fixture, or
  inconsistency across database test, initializer, and installation persistence
  was found.
- One P2 contract gap remained after the first generation: runtime required
  exactly one matching nested driver object, but `DatabaseInput` OpenAPI listed
  all three objects as optional, so generated TypeScript admitted missing,
  mismatched, or multiple driver objects that the API would return 400 for.
- Huma v2 supports a named-type `SchemaTransformer`, `oneOf`, and discriminator
  mapping. The fix is to keep the runtime decoding struct but document it as a
  pure union of three named wrapper schemas, each with a literal driver and one
  required nested value. The base optional properties must not remain, or the
  generated intersection would continue allowing mixed objects.
- The gap is closed: OpenAPI validation rejects missing, mismatched, multiple,
  and unknown driver variants; generated TypeScript emits a three-branch
  discriminated union; compile-only Web assertions protect the generated-client
  behavior. The public schema remains named `DatabaseInput`, so both Setup
  operations share the same strict contract without changing internal DSN
  persistence.

## Cool-Tech Setup Refinement — 2026-08-09

### User Direction

- The user wants the completed Setup redesign changed to a cool color palette
  with a stronger technology character.
- Direction: “polar aerospace instrumentation”—ice-blue canvas, midnight navy
  structural rail, steel-blue controls, and a rare high-chroma cobalt signal.
- Technology character should come from geometric/condensed typography, crisp
  registration lines, tighter corner geometry, and technical background rhythm,
  not neon glow, purple/cyan gradients, glass, or decorative monospace.

### Scope

- The work should remain presentation-only in
  `apps/web/app/setup/commissioning.css` unless a localized typography override
  requires a scoped selector.
- Database verification, administrator creation, initialization polling,
  fail-closed states, English/Chinese catalogs, and the previously verified
  responsive/keyboard contracts remain unchanged.

### C1 Audit Findings

- Warm identity is centralized cleanly: ink/muted hues are aubergine 336,
  canvas/surface/lines are sand 68–72, and signal/focus hues are persimmon
  39–47. Replacing semantic tokens will recolor almost every state without
  selector churn.
- Two hard-coded warm values remain outside tokens: the form shadow uses hue
  336, and the display stack is an Iowan/Palatino/Georgia serif. Both must move
  to the cool system.
- Current roundness (`0.7 / 1 / 1.4rem`) and italic oldstyle numerals reinforce
  the editorial tone. Tighten radii and use tabular condensed numerals with
  upright geometry for aerospace precision.
- Global Simplified-Chinese rules force Setup headings back to Songti. Add a
  route-scoped, higher-specificity sans override so Chinese and English share
  the same technical typographic character without changing other pages.
- Retain semantic green and red states, but cool their light surfaces and tune
  line/control tokens separately so status meaning and 3:1 control boundaries
  remain intact.
- Existing circular registration geometry and three-part rule are purposeful;
  recolor them and add a very low-contrast orthogonal grid to the light canvas
  rather than adding glow, glass, or purple/cyan gradients.

### C2 Design Decisions

- Midnight structural ink: hue 255; ice canvas/surfaces/lines: 245–255; rare
  cobalt signal/focus: 256–260.
- Display stack: DIN Alternate / Avenir Next Condensed / Avenir Next, with
  PingFang SC and other CJK sans fallbacks for Chinese. No new dependency or
  font download.
- Reduce corner radii to `0.45 / 0.7 / 0.95rem` and remove display italics;
  preserve the asymmetric cut-corner form silhouette.
- The first focused CSS check found only cascade-order warnings: the deliberately
  stronger Chinese sans override preceded the base heading rules. Keep that
  specificity, but place the locale override at the stylesheet tail so the
  cascade is explicit and warning-free.
- The first mobile browser script successfully switched locale, tested the
  isolated SQLite DSN, and advanced to the administrator step; only its final
  font-readiness call used the wrong JavaScript context. No installation write
  was sent, so the same fresh Setup instance remains safe to reuse for QA.

### C3 Visual QA Findings

- Desktop at 1920×1080 reads as a cool commissioning console: midnight rail,
  ice-blue grid canvas, and cobalt step/signal accents are distinct without
  glow, glass, or gradient-text effects. Form controls and status hierarchy
  remain clear.
- Mobile Chinese at 390×844 has no horizontal overflow (`390 / 390` CSS px).
  The locale select is 44 px high, visible action buttons are 51 px high, and
  the computed step-heading stack resolves to PingFang SC with the intended
  midnight text color.
- The only visual polish issue is the Chinese rail headline: its inherited
  Latin `ch` max-width separates `控制台` across lines on the narrow layout.
  Give that one CJK heading the available width below the desktop split so the
  product phrase stays intact.
- Browser console reported one anonymous 404 resource during the direct-origin
  preview; identify its URL before classifying it as an application regression.
- The CJK width correction is verified after rebuilding: at 390 px the full
  `启用你的控制台。` headline occupies one measured line (289 px), while page
  scroll width still equals viewport width. The revised screenshot has no
  orphaned product phrase or clipped geometry.
- Network inspection identifies the lone 404 as `/favicon.ico`; all Setup mode,
  locale refresh, CSRF, status, and database requests succeed. The missing
  repository favicon predates and is outside this route-only visual change.
- Final source scan finds no warm editorial font names, legacy warm OKLCH hues,
  or stale `setup.css` imports in the Web app. All three Setup route states load
  the single `commissioning.css` surface.
- Final verification passes: repository Web typecheck/Biome, all 93 Vitest
  cases, the production webpack build with a non-local public API URL, the
  standalone server artifact assertion, and `git diff --check`.

## Modern Setup Redesign Findings — 2026-08-09

### Requirements and Constraints

- The user explicitly requested a modern redesign of the Setup page and named
  the `frontend-design` skill.
- Aginex project guidance routes UI work through `add-admin-page`: preserve API
  contracts, accessibility, responsive behavior, permissions/runtime guards,
  complete data states, and the Web verification gate.
- The worktree already contains substantial uncommitted Setup and localization
  implementation. Those changes are application-owned and must be preserved.
- Current Setup is a three-step client wizard backed by runtime mode/status
  queries and database/setup mutations. The redesign must not change those
  state machines, redirect behavior, or stable API values.

### Initial Visual Direction

- Direction: light editorial-industrial “commissioning desk” rather than a
  generic SaaS card wizard. Use warm mineral surfaces, dark aubergine ink, and
  a rare persimmon signal accent.
- Differentiator: an asymmetric commissioning rail that turns setup progress
  into a strong piece of page architecture, with oversized typographic step
  numerals and a compact live-system status strip.
- Typography should use the project’s already-loaded expressive family where
  possible; avoid adding a network font dependency or falling back to generic
  Inter/Roboto styling.
- Motion is limited to a coordinated entrance and state feedback using
  transform/opacity with exponential easing and a reduced-motion fallback.

### Setup Component Baseline

- `SetupWizard` owns all runtime behavior; the presentational seam begins at
  the returned `<main>` and the four child surfaces (`SetupLedger`, the three
  step components, and probe/error states). The query/mutation/effect block can
  remain untouched.
- Step 1 requires a successful fingerprint-matched database test before the
  continue button unlocks. Changing driver or DSN intentionally invalidates the
  proof and resets the mutation state.
- Step 2 validates matching 12+ character passwords, normalizes the email, and
  immediately starts initialization when submitted. Step 3 polls status,
  renders six machine stages, supports configuration review, and conditionally
  exposes retry.
- Accessibility behavior already includes a skip link, focus transfer to each
  new `<h2>`, visible form labels, field error associations, `aria-current`,
  progress/status announcements, and labeled secret toggles. The redesign must
  strengthen rather than remove these semantics.
- Existing class names are already well scoped under `.setup-page`; most of the
  modern redesign can be delivered by replacing `setup.css` and making only
  small additive markup/class changes for architectural decoration or status.
- Current visual system is an editorial paper ledger with serif headlines,
  yellow active-row highlight, hard rules, and a two-column 18–24rem sidebar.
  It is distinctive but reads archival/print rather than contemporary product
  commissioning, so the redesign should keep editorial hierarchy while moving
  to cleaner surfaces, stronger spatial contrast, and a more modern progress
  rail.

### Style and Platform Baseline

- `setup.css` is a self-contained 895-line stylesheet with desktop, 58rem,
  40rem, 28rem, reduced-motion, and forced-colors branches. Replacing it in
  place is safer than layering overrides because the current selectors already
  cover every Setup state and probe surface.
- Global typography is Avenir Next with native Chinese sans fallbacks; selected
  display surfaces use Georgia or Songti SC. No web font is loaded, so the
  redesign should make an intentional native stack: Avenir Next/PingFang for
  interface copy and Iowan Old Style/Songti for restrained editorial display.
- Global CSS already provides focus tokens, base buttons/inputs, locale
  switcher, spinner, screen-reader utility, and a comprehensive reduced-motion
  override. Setup may specialize these while keeping target sizes at 44px or
  larger.
- The root declares both light and dark color schemes, but Setup intentionally
  sets `color-scheme: light`; this can remain a purposeful light commissioning
  environment instead of inheriting the generic dark theme.
- The existing CSS uses horizontal rule bands and square controls. The new
  direction can feel more contemporary through a full-height dark progress
  rail, a spacious pale canvas, restrained 12–18px corner geometry, offset
  architectural background shapes, and an accented primary action—without
  drifting into generic rounded-card or glassmorphism patterns.
- Probe/loading/error states share the Setup tokens and should be redesigned at
  the same time so initial runtime detection does not flash an unrelated style.

### Copy and Route-State Findings

- All existing Setup English and Simplified-Chinese copy is complete and
  outcome-oriented; the redesign does not need catalog churn. Keeping the same
  DOM text also minimizes E2E selector risk.
- `loading.tsx` and `error.tsx` import the same stylesheet and use
  `.setup-route-loading` / `.setup-probe`, so shared token and composition
  changes will provide a coherent guarded-route experience automatically.
- The long English production database hint and longer Chinese status/stage
  labels are the primary wrapping stress cases. Controls must use `min-width: 0`,
  `overflow-wrap`, and fluid grids rather than fixed one-line assumptions.
- The browser API client uses relative same-origin requests by default, and the
  Playwright configuration starts both the Go server and Next dev server. A
  real fresh-install visual pass is therefore possible after implementation,
  subject to using an isolated temporary installation/config path so existing
  workspace data is not mutated.

### Implementation Boundary

- No unit test asserts Setup class names or DOM shape. Route error/loading
  states use the same class vocabulary and can move to the replacement
  stylesheet by changing only their local CSS import.
- The implementation will replace the existing `setup.css` presentation layer
  with a semantically named commissioning stylesheet and keep the React state
  logic and message catalogs unchanged. This isolates the redesign from the
  large active backend/i18n diff and makes rollback review straightforward.

### Parallel Audit Conclusions

- Setup’s strongest existing foundations—OKLCH colors, logical properties,
  native controls, explicit states, reduced motion, and forced-colors—should be
  retained. The dated feel comes primarily from the archival paper-ledger
  styling, Georgia-heavy hierarchy, desktop-first breakpoints, and extremely
  small mobile metadata.
- The current decorative rule color is only about 1.74:1 against the paper and
  is also used for control boundaries. The new palette must separate subtle
  decorative lines from a roughly 3:1+ control-border token.
- Setup forces a light scheme while global dark-mode tokens still leak into the
  locale select and placeholders. The replacement stylesheet must explicitly
  remap every reused global primitive inside all Setup shells.
- Keep the original radio/fieldset/input/button semantics and every polling,
  normalization, fingerprint, immediate-step-transition, bfcache/focus-refetch,
  and `window.location.replace` behavior. The E2E suite deliberately exercises
  those less obvious paths.
- E2E selectors depend on English accessible names and visible copy, not class
  names. Pure presentation changes are low risk; the current bilingual catalog
  should remain untouched.
- The focused message/runtime/API baseline was already run during the audit:
  three files and all 18 tests passed before the redesign.

### Issues Encountered

- The first focused Biome check found only formatter output in the newly added
  commissioning stylesheet and two compact JSX opening tags. No semantic lint,
  type, or CSS correctness diagnostic was reported; apply the repository
  formatter to the touched files before rerunning validation.

### Isolated Visual-QA Environment

- A fresh Setup runtime is selected solely by an initially absent
  `AGINEX_CONFIG_FILE` together with empty database environment variables.
- Safe browser QA can use dedicated loopback ports plus a unique `/tmp`
  configuration path and upload root. The API public URL and web-origin allowlist
  must match those ports; the Next dev server needs `AGINEX_API_INTERNAL_URL`
  pointed at the isolated API so same-origin `/api/*` rewrites remain valid.
- No database file is created until the Setup form is submitted, so inspecting
  steps 1–2 against the isolated server does not seal an installation or mutate
  existing workspace data.

### Visual QA — Desktop 1920×1080

- The fresh Setup route renders the intended asymmetric composition: dark
  aubergine commissioning rail, warm mineral canvas, rare persimmon signals,
  large editorial step heading, and a single actionable form surface.
- Hierarchy survives the squint test: the ledger title and current step lead,
  the form is clearly second, and disabled Continue remains subordinate to Test
  connection.
- The vertical progress spine, active peach step block, architectural contour
  rings, and tricolor registration mark read as one coherent industrial system;
  no cyan/purple gradient, glass panel, generic metric grid, or nested-card
  pattern is present.
- Form labels, hint copy, driver states, top navigation, and disabled controls
  are legible at the captured desktop size. The main content fits within one
  viewport with ample but intentional negative space and no clipping.
- Next QA targets: mobile viewport, Chinese copy expansion, focus treatment,
  verified connection state, and administrator step.

### Visual QA — Mobile 390×844

- English step 1 and Chinese step 2 both adapt rather than merely shrink: the
  progress rail becomes three compact state tiles, header tools remain
  reachable, the step index/title form a strong two-column lockup, fields stack,
  and actions become full-width.
- All four measured states have `scrollWidth === clientWidth === 390`; there is
  no horizontal overflow. The locale select is exactly 44px high and every
  measured action is about 51px high.
- English and Simplified-Chinese wrapping is clean. Chinese display typography,
  metadata, hints, credential note, and action labels remain legible without
  truncation; the administrator form keeps the hierarchy intact.
- Database verification and navigation to administrator worked through the real
  isolated API, confirming that the presentation changes did not break native
  control behavior or the tested-fingerprint unlock path.
- The apparent skip-link anomaly is confirmed to be only Puppeteer's stitched
  full-page rendering of a fixed element on a scrolled document. In the real
  390×844 viewport the link rectangle is fully off-screen (`top: -84px`), the
  transform is active, focus remains on the step `<h2>`, and the link is not the
  active element.
- The sole 404 is `/favicon.ico`; no Setup/API request failed. This unrelated
  optional browser asset does not affect the redesigned route.
- A real viewport capture at the scrolled Chinese administrator step confirms
  the form, actions, contour decoration, and focused content remain correctly
  aligned with no unexpected fixed UI visible.
- The verified-connection state is visually strong and semantically redundant:
  green surface, shield icon, bold outcome, and supporting text distinguish it
  without relying on color alone. Test again becomes secondary and Continue
  becomes the rare high-chroma primary action as intended.
- Long absolute SQLite paths stay inside the input and scroll horizontally as a
  native single-line field; the overall page still has no horizontal overflow.

### Visual QA — Initialization

- A browser-only intercepted `initializing / migrating` status exercised step 3
  without publishing any configuration. At both 1440×1000 and 390×844 the
  active stage is “Applying schema,” and `scrollWidth === clientWidth`.
- Desktop cleanly balances the step narrative against the progress instrument;
  mobile stacks the complete six-stage spine above the unique dark handoff
  panel while retaining every state and label.
- Complete, active, and pending stages differ through icon/number, border,
  surface, text weight, and color. The animated signal is confined to the
  handoff instrument, so progress remains calm and readable.
- The third progress item activates correctly while the prior two display
  completion checks. All step-3 content fits the viewport on desktop and scrolls
  naturally on mobile with no clipping.

### Accessibility and Preference QA

- With operating-system dark mode emulated, Setup remains intentionally light:
  the page/input/select backgrounds resolve to the local warm OKLCH tokens and
  the container reports `color-scheme: light`. The previous global-token leak is
  fixed.
- With `prefers-reduced-motion: reduce`, both the step panel and ledger entrance
  report `animation-name: none`; functional focus and loading semantics remain.
- Keyboard Tab first reaches the skip link, which completes its short transition
  into the visible viewport at `top: 12px`, retains a solid 3px focus outline,
  and then advances to the explicitly labeled language select.
- An immediate synchronous focus measurement briefly observed the link mid-
  transition above the viewport; the post-transition 200ms measurement confirms
  the final accessible state is correct and this is not a focus defect.

### Visual QA — Fail-Closed State

- A browser-only 500 response for system mode removes the Setup form entirely,
  retains a clear retry action, preserves zero horizontal overflow, and renders
  the guarded error state in the same warm canvas / aubergine / persimmon visual
  language.
- Desktop error copy currently sits too close to the architectural left rail.
  Increase wide-screen inline padding for `.setup-probe` and
  `.setup-route-loading`; keep the compact safe-area padding on mobile.

### Final Change Boundary

- The former tracked `apps/web/app/setup/setup.css` is intentionally replaced by
  `apps/web/app/setup/commissioning.css`; page, loading, and error route imports
  all point to the replacement, and no stale `setup.css` reference remains.
- `setup-wizard.tsx` retains all query, mutation, normalization, polling, and
  redirect logic. Its only changes are additive structural/accessibility state:
  labeled sections, busy/pressed/current attributes, stable heading IDs, and
  presentation data attributes.
- The final temporary QA directory contains screenshots plus a zero-byte
  SQLite test file. No `aginex-config.json` exists, proving the isolated
  installation was never sealed.

### Resources

- `apps/web/components/setup-wizard.tsx`
- `apps/web/app/setup/setup.css`
- `apps/web/app/setup/page.tsx`

## Web Internationalization Findings — 2026-08-09

### Requirements

- Aginex must support multiple UI languages.
- Initial implementation assumption: English and Simplified Chinese, with an
  architecture that makes later locales additive.
- Include locale negotiation, an explicit persistent switcher, localized
  dates/numbers/error copy, and correct `<html lang>` behavior.
- Preserve the existing `/api/v1` contracts and stable problem codes.
- Work around the substantial in-progress Setup changes already present in the
  shared worktree; do not overwrite application-owned drift.

### Initial Hardening Constraints

- Avoid hydration mismatch between server-rendered and client-rendered locale.
- UI controls must tolerate longer translations and CJK text.
- Missing messages must have a deterministic English fallback.
- Locale handling must fail closed to the supported locale allowlist.

### Baseline Discoveries

- The web package has no current i18n dependency. Its root layout is a server
  component with a hard-coded `<html lang="en">`, while `Providers` and most
  interactive surfaces are client components.
- `AppShell` owns navigation, authentication states, environment copy, and
  sign-out UI in one client component, making a context-backed translation hook
  a natural migration seam.
- The workspace layout is dynamic and already performs a server-side runtime
  check before rendering the client shell. Locale resolution can happen once in
  the root server layout and be passed into the provider without adding a URL
  segment or duplicating runtime requests.
- There is no existing translation library in `apps/web/package.json`; adding
  one would require dependency/lockfile churn. A typed in-repo catalog is viable
  for the current surface area and keeps the runtime contract explicit.
- The current shell contains user-visible error, loading, navigation, ARIA, and
  brand-support copy that all needs catalog coverage; permission identifiers and
  route paths must remain unchanged.
- Setup is the largest migration surface (`setup-wizard.tsx` is 941 lines) and
  already contains stable machine states (`stage`, `code`, driver values) next
  to user-facing labels. Catalogs must translate labels and failure fallbacks,
  while preserving every state/code comparison.
- Login, files, and products currently display backend `problem.detail`
  directly. That cannot guarantee a selected UI language; presentation should
  prefer a catalog lookup keyed by stable `problem.code`, with a localized
  generic fallback instead of leaking server prose across locales.
- Dates, counts, prices, and byte sizes are formatted ad hoc with the ambient
  browser locale. Shared `Intl.DateTimeFormat` / `Intl.NumberFormat` helpers
  must use the selected locale explicitly so server language and formatting do
  not drift.
- ResourceList currently derives English grammar from a supplied title
  (`Loading ${title.toLowerCase()}`, `${title} are unavailable`). These states
  need explicit translated messages or message callbacks; sentence composition
  is not portable across languages.
- Status values (`draft`, `active`, `archived`, file/user state), setup stages,
  and permission grant counts need localized display labels without changing
  their underlying API values.
- Dynamic ARIA labels containing filenames/product names require interpolation
  support. Static catalog strings alone are insufficient.
- Product prices are stored as integer cents but the UI hard-codes `$`; the
  selected locale alone cannot infer a business currency. Keep USD as the
  current explicit product assumption and format it with `Intl.NumberFormat`.
- Existing Vitest coverage is library-focused and has no component/i18n tests.
  The configured jsdom + Testing Library stack can cover the provider/switcher,
  while pure locale negotiation/catalog/formatter tests should remain DOM-free.
- Playwright selectors currently target English labels. English must remain the
  default fallback so existing E2E stays stable; add one focused Chinese locale
  scenario rather than duplicating the full destructive admin workflow.
- API error construction should remain language-neutral infrastructure. Add a
  presentation helper outside `api.ts` that maps stable problem codes and HTTP
  statuses into localized UI messages, preserving raw details for diagnostics
  but not using them as the primary translated display.
- Several styles use physical left/right borders and offsets (`app-shell.css`,
  Login, dashboard, files). Full RTL is not an initial shipped locale, but
  locale infrastructure should set `dir` and touched styles should favor logical
  properties so a future RTL catalog does not require architectural changes.
- The root Home and route layouts all render `RuntimeUnavailable`; because the
  root locale provider wraps them, this component can become a client consumer
  without changing fail-closed server mode checks.
- Metadata is currently static English. Root metadata can remain the brand
  fallback, but Setup route metadata should be generated from the server-resolved
  locale to avoid a localized page with an English browser title.
- The installed Next 16 types confirm both `cookies()` and `headers()` are async.
  Locale resolution in the root layout should await both once, prefer the
  allowlisted cookie, then negotiate `Accept-Language`.
- Existing web drift is concentrated in runtime/Setup guards, API contracts,
  shell error handling, and E2E. Internationalization edits must be additive on
  the current file contents; generated `api.generated.ts` is out of scope.
- Repository architecture documentation already classifies `next-intl` as a P0
  frontend dependency, and the v1 PRD explicitly requires English-default UI
  with an internationalization seam. This supersedes the initial no-new-runtime
  preference: use `next-intl` rather than inventing a framework-specific runtime.
- Keep existing unprefixed routes. Locale is presentation state, not an API or
  resource identifier, so cookie/header resolution avoids breaking redirects,
  bookmarks, permissions, and the one-shot Setup E2E workflow.
- Current official `next-intl` App Router guidance explicitly supports apps
  without locale-specific URLs: resolve locale from a cookie or other user
  preference in `i18n/request.ts`, register the Next plugin, and expose request
  config to client components through `NextIntlClientProvider`.
- `next-intl` request config is already React-cache scoped once per request and
  can safely read async `cookies()` / `headers()`. Its provider inherits locale,
  messages, timezone, and formats from the server configuration.
- For unprefixed routing, official guidance changes locale by updating the
  source preference (the locale cookie here). A router refresh can then obtain a
  consistent server/client configuration without changing the pathname.
- Configure a deterministic UTC timezone because audit timestamps are global
  event instants and the product currently has no persisted user timezone.
- The backend Problem contract already guarantees a language-neutral `code` but
  treats `title` and `detail` as free text. Many business errors collapse to
  generic status-derived codes, so the first UI mapper can promise localized
  generic guidance, not a distinct translation for every backend cause.
- Setup exposes a finite status/stage contract and explicit `SETUP_*` codes;
  translate their presentation without changing request payloads, React Query
  keys, OpenAPI, or generated client types.
- Native HTML validation bubbles follow the browser UI language, not the
  in-product cookie. Initial i18n will retain native validation semantics; fully
  controlled translated validation is a separate form-validation enhancement.
- Role descriptions and audit summaries are backend-owned English content.
  Localizing that data later requires stable structured keys/parameters, not
  translated database columns; this slice localizes product chrome and enum/action
  presentation without claiming backend content localization.
- `next-intl` installed successfully through the existing pnpm store. The
  package manager reported only already-policy-managed ignored optional build
  scripts; no application build script was requested or bypassed.
- The resolved version is `next-intl` 4.13.5, whose peer range explicitly
  includes Next 16 and React 19. Its current types support `AppConfig` module
  augmentation for strict Locale and Messages types.
- Locale request configuration now uses explicit message loaders, the strict
  cookie allowlist before header negotiation, and UTC. The root server layout
  consumes the same request locale for `<html lang dir>` and the client provider.
- The finished catalogs contain exact recursive and ICU placeholder parity for
  English and Simplified Chinese, including ARIA text, loading/error states,
  enum labels, plural counts, Setup stages, and stable Problem codes. User-entered
  and backend-owned content remains verbatim by design.
- The locale switcher persists only an allowlisted, non-sensitive BCP 47 value
  in a one-year SameSite=Lax cookie (Secure on HTTPS), updates document language
  and direction immediately, then refreshes Server Components so metadata and
  provider messages converge on the same request locale.
- Locale-sensitive CSS now uses logical inline properties for the shell, tables,
  form errors, metrics, and upload accents; mobile table labels can expand to
  40% and wrap, and Simplified Chinese receives an explicit CJK font stack with
  reduced editorial letter spacing.
- A separate problem presentation helper maps stable codes to catalog keys,
  supports operation-specific overrides, and intentionally never exposes raw
  backend detail as the localized default.
- Production-request verification confirms that request negotiation controls the
  initial document language, route metadata, and Setup copy before hydration;
  the strict locale cookie takes precedence over a conflicting browser header.
- Final Web verification passed typecheck, full Biome, 93 Vitest cases, the
  webpack production build, standalone artifact assertion, Playwright test
  discovery, and whitespace validation. The destructive one-shot Setup/admin
  Playwright workflow remains an environment gate rather than being run against
  the developer's current installation state.

### Resources

- https://next-intl.dev/docs/getting-started/app-router
- https://next-intl.dev/docs/usage/configuration
- https://next-intl.dev/docs/usage/translations
- https://next-intl.dev/docs/usage/dates-times

## Embedded Setup Findings — 2026-08-09

- `cmd/server` currently loads a fully validated runtime config, opens the
  database, constructs the application, and only then starts HTTP; it needs a
  database-independent supervisor and base-config loader.
- `config.Load` currently defaults a missing database to
  `sqlite/data/aginex.db`, so raw environment presence must be tracked before
  defaults are applied.
- Reusable initialization seams already exist:
  `Definition.MigrateUp`, `Definition.Bootstrap`, `Definition.NewAPI`, and
  `App.Start`.
- The web root unconditionally redirects to `/dashboard`; workspace auth treats
  every `/auth/me` failure as unauthenticated. Both must use a no-store system
  mode probe and distinguish 401 from availability failures.
- The current Docker API image injects a SQLite DSN, which would permanently
  bypass Setup; that default must be removed and `/data` must carry the sealed
  configuration.
- Production FilesModule currently requires PostgreSQL jobs, so the shipped
  production composition can only complete Setup with PostgreSQL unless Jobs
  or the module composition changes outside this task.
- Setup is an unauthenticated database-connection surface by explicit product
  choice. Strict Origin/CSRF, bounded requests, redacted errors, and an external
  VPN/proxy/security-group allowlist are mandatory compensating controls.
- UI direction: refined industrial installation console, three steps, visible
  progress, no decorative card grid, responsive split composition, strong
  focus states, and reduced-motion support.
- Existing bootstrap always performs upserts/association replacement and emits
  `SourceCLI`, even on an unchanged second run. Main-owned startup requires a
  drift preflight plus configurable `http`/`system` audit context so restarts
  do not create misleading audit entries.
- `App.Start` is idempotent while started and `App.Shutdown` owns reverse-order
  lifecycle cleanup, so a Setup initializer can start a candidate before the
  atomic handler swap and hand a single cleanup closure to the supervisor.
- Worker already uses configured-only `config.Load` and performs read-only
  readiness checks; once that loader understands the installation file it can
  remain non-mutating while failing with `ErrSetupRequired` on fresh installs.
- The installation store uses an exclusive hard-link publication after file
  fsync, which prevents overwrite races. Its API must distinguish failures
  before publication from cleanup/directory-sync failures after the target is
  visible; otherwise the live handler could remain in Setup while the next
  restart is permanently configured.
- HTTP readiness already contains the authoritative database, migration,
  storage, and module checks, but lacked a callable method. Candidate Setup
  activation needs the same checks before sealing configuration, so `App.Ready`
  is being exposed without changing the public probe response.
- The Setup supervisor now separates a stable mode route from an atomically
  swappable delegate, runs accepted initialization under a process-owned
  timeout, clears request-held secrets after use, and treats configuration
  commit as the point of no return. Activated candidates must require a
  shutdown closure so lifecycle/database resources remain owned through server
  shutdown.
- Setup captures only normalized request ID and client IP into its detached
  initialization context. The server initializer can therefore emit HTTP
  bootstrap audit attribution without retaining arbitrary headers or tying the
  accepted task to the browser request context.
- Setup exports a pure `DocumentOpenAPI` contract merger. Calling it only from
  `application.Definition.BuildOpenAPI` publishes the union contract to codegen
  without registering Setup operations on a normal application router.
- The Setup HTTP surface reuses the framework request-limit, CORS, CSRF, trusted
  proxy, request-ID, RFC Problem, and no-store primitives. The mode probe is
  intercepted outside both delegates, while Setup health is deliberately ready
  so trusted ingress can route a fresh installation.

## Market

- `gin-vue-admin` and `go-admin` validate demand for batteries-included Go
  admin frameworks, but are Vue-first and do not provide a portable,
  measurable Coding Agent workflow.
- Refine and React Admin validate demand for typed, composable React admin
  primitives, but do not provide an integrated Go backend contract.
- AdminForth uses "Agent-first" for runtime business agents. Aginex must
  consistently describe itself as Coding-Agent-first development
  infrastructure.
- Agent Skills is an open specification with progressive disclosure through
  `SKILL.md`, `references/`, `scripts/`, and `assets/`.

## Architecture

- Gin remains the HTTP router and middleware host.
- Huma v2 can use the official Gin adapter and generate OpenAPI 3.1/3.0 from Go
  input/output types, reducing annotation and client drift.
- GORM is selected for ecosystem familiarity and multi-database support.
- Goose is selected for explicit, reviewable production migrations; GORM
  `AutoMigrate` must not run in production.
- RBAC will use explicit relational tables and `resource:action` identifiers in
  v1. Casbin is deferred until ABAC or data-scope requirements justify it.
- Browser authentication uses revocable server-side sessions and secure
  cookies, not browser-stored JWTs.

## Dependency Risks

- The default SQLite path should avoid CGO for easy local onboarding.
  `github.com/glebarez/sqlite` is the planned GORM dialector, but it must pass
  the Go 1.25/1.26 and full GORM behavior matrix before release. The official
  CGO driver remains an opt-in fallback.
- Alibaba Cloud OSS must use the official v2 SDK rather than the legacy SDK.
- S3-compatible providers share the AWS SDK v2 adapter; a separate MinIO SDK is
  unnecessary for v1.

## Scope Boundaries

- No multi-tenancy, dynamic database menus, visual low-code builder, workflow
  engine, plugin marketplace, image transformations, or runtime LLM agent in
  v1.
- English is the product language. These planning documents are Chinese-first
  so product decisions can be reviewed efficiently; public guides can be
  translated after the implementation stabilizes.

## Production Readiness Assessment (2026-07-30)

- The repository is a functioning early vertical slice, but its current green
  checks do not establish multi-user production safety.
- `App.New` currently applies migrations, bootstraps data, constructs storage,
  and registers all routes. Composition, migration, and contract generation
  need independent side-effect-free entry points.
- File metadata already has an owner, but list, signed-read, local-content,
  local-upload, and delete paths do not consistently enforce ownership.
- Successful business writes and audit inserts use separate transactions; audit
  failure currently logs and allows the business write to remain committed.
- Most business routes are registered with Gin and manually mirrored into
  OpenAPI without request, response, error, query, or security schemas. The
  generated TypeScript client is not the frontend's actual contract.
- Opaque browser session tokens are random and hashed at rest, but CSRF,
  redirect validation, proxy trust, request limits, shared rate limiting, and
  production configuration checks are incomplete.
- The storage abstraction, opaque keys, size/type policy, and path validation
  are useful foundations. Deep image verification, checksum, reliable cleanup,
  and live provider contracts are missing.
- A PostgreSQL-only queue cannot be a framework invariant while SQLite and
  MySQL remain supported. Durable jobs belong behind an opt-in module/provider
  contract; POSTA may select the PostgreSQL provider.
- The nested `{error:{...}}` proposal conflicts with RFC Problem Details, and a
  universal cursor-page switch conflicts with the existing admin list contract.

## Remediation Architecture

- Core packages will expose compile-time module registration, typed operations,
  actor/authorizer contracts, transactional audit, migration version checks,
  safe HTTP middleware, and storage lifecycle primitives.
- Optional packages will provide public token authentication, idempotency, and
  durable job processing without forcing their tables or processes into every
  application.
- POSTA owns SMS/Apple adapters, domain resources, custom ownership rules,
  quota values, abuse thresholds, task payloads, and operational retention
  policies.
- The framework delivery model must be made explicit. Until reusable public Go
  packages or `aginex new` exist, a second application can only fork/copy the
  repository despite the documented module goal.
- The existing handlers can migrate incrementally to atomic audit without
  changing database dialect behavior: a GORM unit of work accepts a mutation
  that returns its sanitized audit event, records that event through the same
  transaction handle, and commits only after both succeed.
- Authentication service methods currently own their database handle directly.
  Atomic login/logout audit will require transaction-aware service methods or a
  repository method that accepts the transaction supplied by the unit of work.
- Only `internal/app` calls `auth.Service.Login` and `Logout`, so adding
  transaction-aware variants can preserve compatibility wrappers without
  affecting another package API.
- The new module registry establishes fail-closed operation metadata and
  deterministic composition, but its operation definition intentionally does
  not yet carry Go request/response DTO types. Typed Huma binding remains an R3
  integration task rather than being implied by the registry alone.
- Migration status/version inspection is now read-only on an empty database and
  uses an instance Goose provider instead of process-global dialect/FS state.
  Cross-process migration locks remain outstanding and must be implemented per
  supported adapter without leaking dialect logic into services.
- The authorization package keeps `system` as an actor kind and requires
  explicit system permissions; no human administrator receives an implicit
  system bypass.
- Built-in HTTP routes can now be mounted from registered operation metadata,
  which removes permission drift at the router boundary. Object/query policy
  enforcement is deliberately still a separate runtime step and remains part
  of R2.
- Registry composition is side-effect-free and reusable by OpenAPI generation;
  the current OpenAPI document still manually describes most Gin operations,
  so the registry alone does not satisfy the typed-contract R3 gate.
- Goose 3.26 already exposes a PostgreSQL session locker and runs SQL
  migrations on the lock-owning dedicated connection. Using that provider
  option is safer than maintaining a parallel advisory-lock implementation.
- Safe image verification can remain provider-neutral: a bounded reader,
  signature-derived MIME type, format container checks, decode-config limits,
  full decoding, and content hashing do not require a cloud SDK or database.
  The new primitive deliberately remains separate from upload handlers until
  the file lifecycle can persist all verified metadata atomically.
- File API contracts must be projections rather than JSON-tagged persistence
  models. The public file projection intentionally omits bucket, object key,
  ETag, and owner ID, while signed upload/read data has its own narrow response
  contract.
- Object-store verification should remain outside the database transaction, but
  confirmation must re-read and re-authorize the intent before atomically
  persisting verified metadata and its audit event. This avoids holding a
  database transaction across remote I/O without trusting stale intent fields.
- A portable shared limiter does not need service-layer dialect branches:
  storing a revision with each fixed window permits compare-and-swap updates,
  while GORM renders the duplicate-safe insert for SQLite, PostgreSQL, and
  MySQL. An immutable random insert token avoids trusting dialect-dependent
  `RowsAffected` behavior on duplicate inserts. Denials are fail-closed and
  never increment the counter.
- Rate-limit state belongs to an opt-in module rather than the core schema.
  Embedded dialect migrations use their own Goose history table, so enabling
  or removing the module cannot advance the application's schema version.
- Persisting only a bounded SHA-256 bucket identifier keeps raw IP, email, or
  account keys out of the shared state table. Namespace, subject key, and
  window duration define independent buckets; application projects still own
  concrete thresholds and abuse policy.
- Durable job dispatch can consume the module registry without a package cycle:
  `module.JobHandler` deliberately depends only on `context` and JSON, while
  the runtime edge in `framework/jobs` imports the immutable registry snapshot.
  Retry timing remains provider-owned through `Queue.Fail`; the runner owns
  exact type/version selection, lease heartbeats, bounded execution, and
  fail-closed settlement.
- Portable HTTP idempotency does not require dialect-specific service logic:
  a SHA-256 scope over actor, method, route, and key plus revisioned
  compare-and-swap transitions grants one execution lease across SQLite,
  PostgreSQL, and MySQL. Lease capabilities and client keys are hashed at rest,
  while expiry permits bounded recovery without accepting a stale completion.
- Safe replay is intentionally narrower than arbitrary response recording.
  Non-empty bodies must be JSON, sensitive field names and signed URLs are
  rejected, and only bounded non-secret headers can be stored. Applications
  still own canonical request-digest construction, route opt-in policy, lease
  renewal for long requests, and mapping in-progress/conflict outcomes to HTTP.
- Idempotency state is an opt-in module with isolated Goose history. A
  transaction-bound store can complete the replay record with the business
  write, while default 5xx handling releases the claim instead of caching an
  infrastructure failure.
- API token authentication can remain isolated from browser sessions and the
  core schema. A pinned HS256 access contract carries issuer, audience,
  subject, expiry, family, and device identifiers; rotating opaque refresh
  tokens persist only a separate-key HMAC. A conditional active-to-used update
  plus child insert makes rotation portable and atomic across the three
  supported SQL dialects.
- Refresh replay evidence must outlive every token it can invalidate. The
  token module therefore retains used rows and revokes all active descendants
  when an old token is presented. Projects must not purge used rows before the
  maximum refresh lifetime, and must add a family-age policy if they do not
  want the supplied sliding refresh expiry.
- Stateless family logout cannot immediately invalidate an already-issued
  access token without an introspection/deny-list read. The core instead keeps
  access TTL short and always rechecks caller-owned user status; POSTA may add
  per-device introspection if its threat model requires immediate access-token
  revocation. HTTP transport, rate limiting, authorization, and audit remain
  adapter responsibilities.
- Audit actor identifiers cannot share the UUID-only constraint of user IDs:
  worker IDs and service identities are framework actors too. The public audit
  contract already permits 160 characters, so the database and GORM model must
  use that same bound or PostgreSQL rejects valid worker audit events.
- Resource metadata should be declarative and immutable, but it should not
  generate runtime CRUD through reflection. Requiring ownership, policy,
  operation references, DTO types, and explicit audit/sensitive field lists at
  registration gives generators a fail-closed input while services retain
  control over invariants and SQL-level scope.
- HTTP idempotency is optional but defaults to the portable database provider
  because the published write contract advertises `Idempotency-Key`. Projects
  may explicitly disable the module; a request that still sends the header then
  fails with a stable 503 problem instead of executing while silently ignoring
  the caller's safety expectation.
- The six built-in idempotent writes bind a key to the authenticated actor,
  method, registered route template, actual path, canonical query, content
  type, and exact request body. Their successful business mutation, audit row,
  and idempotency completion share one transaction. The initial execution claim
  necessarily precedes that transaction; a crash before the transaction
  commits is recovered by lease expiry and cannot leave a completed business
  write without its replay record.
- Future application-owned idempotent handlers must call the transaction-bound
  completion helper from their write transaction. The middleware has a
  fail-closed post-handler completion fallback for successful no-op paths, but
  using that fallback for a new business mutation would reintroduce a
  commit-to-completion crash window and must not be described as exactly once.
- Upload-intent replay deliberately stores only the public file projection and
  an empty upload descriptor. Every replay re-authorizes the pending file and
  asks the storage adapter for a fresh short-lived upload request, so signed
  URLs and signed headers never enter the idempotency table.
- Runtime composition still invoked bootstrap after its read-only schema
  check, so every API start could write permissions and administrator
  associations. The bootstrap entry point can derive the SQL dialect from
  GORM, reuse the built-in registry permissions, enforce the current schema
  with a read-only check, and run every seed mutation plus one CLI/system audit
  through the existing unit of work.
- Reading and revoking one's own browser sessions is an identity lifecycle
  capability rather than an application-role privilege. Injecting the two
  registered session permissions as intrinsic `own` grants after successful
  authentication keeps route middleware fail-closed while allowing every
  active user to access `/me`, current-device logout, and all-device logout.
- All-device logout does not need a caller-selectable resource identifier.
  Deriving the deletion scope exclusively from the authenticated principal's
  user ID prevents horizontal access, and sharing the audit unit-of-work
  transaction guarantees that an audit failure restores every deleted session.
- A direct-upload intent needs its own durable expiry delivery at the moment
  the metadata row is created. Scheduling `storage.cleanup` v2 for the signed
  request expiry plus a two-minute grace in the same file/audit transaction
  prevents committed pending rows from losing their recovery task.
- Cleanup delivery identity must include the lifecycle cause. A stale
  `pending-expiry` delivery is allowed to succeed as a no-op after confirmation,
  so explicit deletion and invalid-upload cleanup use different idempotency
  keys; otherwise the succeeded stale job could suppress a later required
  deletion.
- Pending expiry must lock and re-read metadata before touching storage, and
  confirmation must likewise require a locked `pending` row. This prevents a
  delayed expiry from deleting a ready object and prevents a confirmation
  racing after expiry from resurrecting deleted metadata.
- Object authorization metadata is insufficient unless runtime use can be
  verified. Holding successful responses until a registered handler consumes
  its declared all/actor/object/query mode prevents accidental unscoped output.
  It cannot undo arbitrary raw database side effects, so application modules
  must treat Gin handlers as trusted adapters and perform mutations through the
  injected audited unit of work.
- Compiled-in modules need runtime dependencies without importing Aginex
  `internal` packages. An immutable context snapshot of GORM, audited writes,
  object storage, optional transactional jobs, and observability keeps
  registration side-effect free and makes handlers, readiness, lifecycle, and
  worker deliveries testable without globals.
- W3C header validation and log correlation alone are not distributed
  tracing. The durable boundary must create a producer span, persist that exact
  child context atomically with the job, extract it in the worker, and create a
  consumer span before database or storage calls.
- Framework telemetry should define stable low-cardinality semantics while
  deployment code owns exporters. Keeping the recorder and sink provider
  neutral avoids global OTel state and mandatory infrastructure; POSTA still
  owns its business metrics, SLOs, dashboards, sampling, retention, and
  provider credentials.
- API readiness should verify only dependencies needed to serve API traffic;
  requiring a worker to be online would cause false failures during rolling
  deploys. Worker readiness is a separate bounded decision, while backlog,
  oldest-job age, stale leases, and dead jobs belong in metrics and alerts.
- A validated upload intent is not a size boundary unless the declared byte
  count participates in the cloud-provider signature. S3 accepts
  `PutObjectInput.ContentLength`; OSS V4 requires `Content-Length` as an
  explicitly configured additional signed header, so the OSS adapter owns that
  signer configuration instead of relying on callers to remember it.
- Built-in file deletion, invalid-upload cleanup, and pending-intent expiry are
  durable workflows. A production composition that registers `FilesModule`
  must reject `Jobs.Driver=disabled`; a zero-business composition does not
  acquire that dependency. Development and test configurations may still
  disable the worker for narrow local use.

## Final Remediation Decisions (2026-07-31)

- Framework composition is exact, not additive by accident:
  `application.Define()` has no business schema or route, while
  `FilesModule()` and `StarterExampleModule()` are explicit selections. The
  repository reference application selects both so the shipped web and E2E
  example remain functional.
- Every process consumes the same immutable definition and module fingerprint.
  API, Worker, migrate, bootstrap, and OpenAPI therefore cannot silently
  disagree about routes, permissions, migrations, or background handlers.
- Bundled module migrations have independent Goose histories, support empty
  installation and pre-stable adoption, and fail closed when only part of a
  legacy schema is present.
- Browser protocol names are framework constants. Runtime cookie/session/CSRF
  behavior and OpenAPI now share `aginex_session`, `aginex_csrf`, and
  `X-CSRF-Token`; production configuration cannot rename one side and cause
  contract drift.
- Shared rate limiting is a core provider contract because authentication and
  sensitive endpoints require cross-instance enforcement. Policy values and
  business-abuse detection remain application concerns.
- POSTA should implement its postal domain resources, custom ownership graph,
  SMS/Apple/WeChat identity adapters, quota values, abuse rules, business jobs,
  publication policy, and MFA user experience on top of these contracts.
- Source qualification is complete locally. Stable-release qualification is a
  separate evidence decision and still requires the live database, object
  storage, browser, container, SBOM, and security-scan gates configured in CI.

## Local Toolchain

- Go 1.25.3 on darwin/arm64.
- Node.js 22.19.0 and pnpm 10.33.2.
- Docker 29.4.0.
- Git 2.50.1; the workspace is an initialized repository with an intentionally
  uncommitted remediation worktree.
# Embedded Setup implementation review (2026-08-09)

- The Setup supervisor owns a single atomic `{mode, handler}` pair, detaches accepted initialization from the browser request, commits installation state before switching to the candidate handler, and treats that commit as the irreversible boundary.
- Setup responses, mode probing, application-mode Setup 404s, and setup health/CSRF routes are all emitted through no-store handlers. Setup request DTOs use finite enums and write-only DSN/password schema fields; dependency failures are mapped to stable generic codes.
- The in-memory write limiter is bounded and keyed by validated route plus trusted client IP, matching the locked single-instance assumption.
- The shared CSRF middleware accepts an allowlisted Referer when Origin is absent. The locked Setup contract is stricter, so Setup write routes need an additional explicit non-empty Origin requirement before invoking the operation.
- `App.Start` receives the initializer's bounded context. Lifecycle hooks are permitted to retain that context, so canceling the initialization timeout immediately after activation could terminate background hook work. Startup needs a process-lifetime runtime context while database/migration/readiness operations remain timeout-bounded.
- Installation state correctly separates managed DSN persistence from environment markers, rejects partial database environment configuration and unsafe target files, and publishes an exclusive 0600 file only after syncing its contents. The marker is written only after a configured candidate is ready.
- Delivery now builds only API/worker/web images, gives Go runtimes a persistent `/data` installation volume, and no longer injects a browser API origin at Web build time. Server-rendered Web guards therefore depend on the documented runtime-only `AGINEX_API_INTERNAL_URL` whenever Next and Go are separate containers.
- Initial delivery prose covered same-origin browser routing but omitted the server-only internal API variable and `/health/*` ingress split; this was sent back for correction before final verification.
- The existing Playwright suite still starts an already configured/bootstrap-backed application and contains no committed first-installation scenario. To meet the locked acceptance matrix, CI must start with an absent isolated config file and let the browser submit PostgreSQL/admin Setup before the existing admin workflows.
- Adversarial frontend review found cross-tab CSRF invalidation: every token GET replaces the shared cookie, while each tab caches its token promise indefinitely. A second tab can make the first tab's Setup completion permanently 403. The backend should reuse a valid cookie token, and the browser client should invalidate/retry once on `CSRF_FORBIDDEN`.
- Backend adversarial review found four fail-closed gaps to close before completion: pre-middleware business-path 404 isolation in Setup, Setup-specific size/TTL clamps, shutdown racing a delayed successful commit, and distinguishing uncommitted retryable config failures from sealed/published conflicts. It also found one secret-retention issue (bootstrap password copied into long-lived App config) and context.Background calls that bypass the total initialization timeout.
- The first three supervisor gaps are now closed and covered by race tests. The remaining irreversible-boundary work is the config store's sealed-vs-retryable result and supervisor behavior when an installation file appears or is published without a confirmed directory fsync.
- The config store now exposes `InstallationSealed(err)`: EEXIST and post-publication directory-fsync errors are non-retryable sealed outcomes, while pre-publication failures remain retryable. New directories sync their parent and the destination directory is preflight-fsynced before publication.
- The current shipped definition includes Files, whose production constructor requires `AGINEX_JOBS_DRIVER=postgres`; config validation in turn requires a PostgreSQL database for that job driver. Therefore SQLite/MySQL may still be tested in development or derived compositions, but the production distribution rejects them before the installation file commit boundary.
- The Setup supervisor initially reports database validation, advances to migration after its own ping, and accepts detailed stage reports from the unified initializer. Progress reports are now clamped to a monotonic stage order, so the concrete initializer's defensive database reopen cannot make the browser progress display move backward.
- A worker needs an affirmative API lifecycle signal, not merely a readable installation marker: the marker proves a prior initialization but does not prove the current API completed a new automatic migration. Worker startup therefore polls the DB-independent mode endpoint and readiness before opening the database; its public API URL must be routable from the worker network.
- HTTP Setup retries need different credential semantics from normal access drift synchronization. Setup explicitly replaces and verifies the submitted administrator hash so a failed pre-commit attempt can change password; configured startup still preserves existing credentials and only writes when permissions/roles drift.
- Installation publication must classify more than the exclusive rename itself. Any filesystem failure before publication now rechecks the destination: only a definite absence remains retryable, while an existing or uninspectable marker seals Setup; new directory hierarchies persist each component by syncing its parent before proceeding.
- Candidate ownership needs a durable in-memory shutdown result as well as an atomic handler. Both sealed and activated candidates now have exactly-once cleanup barriers; active cleanup uses a Supervisor-owned bounded context, while concurrent or retried callers independently bound their wait and observe one stable sanitized result.
