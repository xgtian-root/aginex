# Aginex

<p align="center">
  <strong>A framework for AI agents building auditable business applications with Go and Next.js.</strong>
</p>

<p align="center">
  Read the task, select a Skill, implement against explicit contracts, and
  verify the result with machine-checkable gates.
</p>

<p align="center">
  <a href="https://github.com/xgtian-root/aginex/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xgtian-root/aginex/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/xgtian-root/aginex/blob/main/LICENSE"><img alt="Apache-2.0 license" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <img alt="Go 1.25.12+" src="https://img.shields.io/badge/Go-1.25.12%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Node.js 22.23.2" src="https://img.shields.io/badge/Node.js-22.23.2-5FA04E?logo=nodedotjs&logoColor=white">
</p>

> [!IMPORTANT]
> Aginex is currently an early development baseline, not a stable release.
> Authentication, RBAC, audit logs, product CRUD, image uploads, the admin
> shell, contract generation, and Agent Skills are implemented. APIs and
> project conventions may still change before v1.

## What is Aginex?

Aginex is an agent-first application framework. It gives you, an AI coding
agent, a constrained and inspectable way to create and evolve administrative
and operational business software without having to infer the project's
security model, architectural boundaries, or definition of done.

The framework combines an executable Go and Next.js baseline with repository
instructions, task-specific Agent Skills, typed contracts, and verification
commands. The development workflow is part of the framework, not knowledge
that exists only in a maintainer's head.

Use Aginex when your task needs one or more of these capabilities:

- CRUD resources and business-specific operations such as publish, archive,
  approve, retry, or moderate.
- Permission-aware admin pages, dashboards, reports, and settings.
- Revocable browser sessions, explicit role-based access control, and an audit
  event for every successful write.
- SQL schemas that evolve through reviewable Goose migrations on SQLite,
  PostgreSQL, and MySQL.
- Image and object workflows backed by local storage, S3-compatible services,
  or Alibaba Cloud OSS.
- Durable PostgreSQL jobs, idempotent HTTP operations, health checks, and
  production-oriented container images.
- A typed boundary from Go HTTP APIs to a generated TypeScript client used by
  the Next.js application.

Aginex supplies the security, transaction, storage, job, contract, migration,
and telemetry boundaries. You supply the application's domain rules. It is not
a reflective CRUD engine and must not be used to bypass business-specific
service logic.

## How to use Aginex as an Agent

Start every task from the repository root and follow this sequence:

1. Read [`AGENTS.md`](AGENTS.md) and any nearer `AGENTS.md` that applies to the
   files you will change.
2. Classify the task and read the matching Skill in [`.agents/skills/`](.agents/skills/)
   completely before editing code.
3. Inspect the existing module, API, migration, permission, audit, generated
   contract, and test patterns relevant to the task. Preserve application-owned
   changes and detect generated-file drift before overwriting anything.
4. Implement the smallest complete vertical change described by the Skill.
   Keep domain logic in services and use framework interfaces at infrastructure
   boundaries.
5. Run the Skill's focused checks, regenerate contracts when required, then run
   the repository completion gate:

   ```bash
   go run ./cmd/aginex check
   ```

6. Report the changed behavior, migrations or generated artifacts, verification
   performed, and any remaining risk. Do not claim completion while a required
   gate is failing or skipped.

Select the Skill by task intent, not merely by the file being edited:

| Task intent | Read this Skill |
|---|---|
| Create and initialize a new Aginex application | `create-aginex-project` |
| Add a CRUD entity or business module | `add-business-resource` |
| Add publish, archive, approve, retry, or another non-CRUD operation | `add-custom-api-operation` |
| Add a dashboard, report, settings screen, or other admin page | `add-admin-page` |
| Change a table, column, index, constraint, or data migration | `change-database-schema` |
| Add or modify roles, permissions, or protected actions | `configure-rbac` |
| Add an image upload flow or storage integration | `add-image-upload` |
| Diagnose a bug, CI failure, migration problem, or readiness issue | `test-and-debug` |
| Upgrade framework dependencies or conventions | `upgrade-aginex` |

If a task crosses several rows, read every applicable Skill and combine their
completion gates. The Skills encode implementation order, required tests, and
project-specific constraints; this README is the entry point, not a substitute
for those instructions.

## Contracts you must preserve

Treat these as non-negotiable unless the task explicitly changes the framework
contract and updates its tests and documentation:

- Goose SQL migrations are the schema authority. Never introduce `AutoMigrate`.
- Keep service code database-dialect neutral; support SQLite, PostgreSQL, and
  MySQL through the migration and platform layers.
- Browser authentication uses revocable server-side sessions.
- Permissions are lowercase `resource:action` identifiers and are enforced by
  the API, not only represented in the UI.
- Every successful write produces an audit record in the same transactional
  unit as the business change.
- Application HTTP APIs live under `/api/v1`, errors use
  `application/problem+json`, and lists use `{items,page,pageSize,total}`.
- Cloud SDKs stay behind platform interfaces.
- Generated and application-owned files must not be overwritten without first
  checking for drift.

## What is included?

| Area | Current baseline |
|---|---|
| Identity | First-run administrator setup, Argon2id passwords, revocable cookie sessions |
| Authorization | Explicit RBAC with lowercase `resource:action` permissions |
| Audit | Actor, action, resource, request, and timestamp records |
| Business example | Searchable, paginated product CRUD |
| Files | Upload intent, direct upload, verification, signed reads, and deletion |
| Reliability | Database-backed idempotency, a durable PostgreSQL worker, and protected job inspection/retry APIs |
| Admin UI | English/Simplified-Chinese login, Setup, dashboard, products, users, roles, audit, and files pages |
| API contract | `/api/v1`, RFC 9457-style problem responses, OpenAPI, generated TypeScript client |
| Delivery | Non-root API, worker, and standalone web images |
| Tooling | `dev`, `doctor`, `check`, contract generation, security gates, SBOMs, and Skill validation |

The web app uses English by default, negotiates a first visit from the browser's
`Accept-Language` header, and stores an explicit language choice in the
allowlisted `aginex_locale` cookie. UI language does not change route paths,
permission identifiers, API problem codes, or user-entered business data.

## Architecture

```text
Browser ──► Next.js web ── generated client ──► Go API (/api/v1)
                                                   │
                                  ┌────────────────┴──────────────┐
                                  ▼                               ▼
                         SQL database                       Object storage
              business/audit/session/idempotency            Local/S3/OSS
                       SQLite/Postgres/MySQL                       ▲
                                  ▲                               │
                                  └──── PostgreSQL worker ─────────┘
                                           durable jobs
```

The service layer stays database-dialect neutral. Schema differences live in
explicit Goose migrations, while cloud-specific SDKs stay behind storage
interfaces. The API applies pending migrations and synchronizes built-in access
records before it reports ready.

## How Aginex applications are composed

A derived application defines one immutable module set and reuses it for API,
worker, migrations, bootstrap, and OpenAPI generation:

```go
definition, err := application.Define(
	posta.PostmarksModule{},
	posta.PublicationModule{},
)
if err != nil {
	return err
}

recorder, err := observability.NewRecorder(deploymentSink)
if err != nil {
	return err
}
definition, err = definition.WithObservability(recorder)
```

`application.Define()` is intentionally zero-business: it includes the shared
identity, session, RBAC, audit, health, jobs administration, storage, and
request-safety primitives, but it does not add a product model, dashboard, or
file-object API/schema. This repository's starter distribution opts in
explicitly:

```go
definition, err := application.Define(
	application.FilesModule(),
	application.StarterExampleModule(),
)
```

`FilesModule` is the official optional file-object HTTP/schema module.
`StarterExampleModule` contains only the sample products resource and its
product-backed dashboard; derived applications normally replace it with their
own business modules.

Each `module.Module` registers its routes and middleware, permission and
authentication definitions, object/query authorization policies, typed API
contracts, migration bundles, durable job handlers, readiness checks, and
lifecycle hooks. Registration is deterministic and side-effect free. A
protected operation cannot start unless all of those cross-references are
complete, and a successful response is rejected if its handler did not consume
the declared `all`, `actor`, object, or query authorization mode.

Module handlers resolve a `services.Runtime` from their context. It contains
the portable GORM database, atomic `Writes` unit of work, public object-store
contract, optional transactional job queue, and shared observability recorder.
All business mutations must run through `Runtime.Writes.Run` and return a
sanitized `audit.Event`; a failed audit insert rolls the business transaction
back. Raw Gin handlers are therefore a trusted low-level adapter, not an
alternative write path.

`ResourceDefinition` is generator and validation metadata, not a reflective
CRUD engine. A derived application remains responsible for its domain rules;
Aginex provides the boundaries those rules build on.

## Run the baseline locally

### Prerequisites

- Go 1.25.12 or newer
- Node.js 22.23.2
- pnpm 10.33.2
- Docker (optional, for PostgreSQL, MySQL, or MinIO)

### 1. Clone and configure

```bash
git clone https://github.com/xgtian-root/aginex.git
cd aginex
cp .env.example .env
```

The example leaves the database and administrator unset so the first API start
opens browser Setup. It also leaves the session secret empty; Aginex generates
one and persists it with the installation state.

### 2. Install dependencies

```bash
pnpm install
```

### 3. Start the API and web app

```bash
set -a
source .env
set +a
go run ./cmd/aginex dev
```

### 4. Complete first-run Setup

Open <http://localhost:3000>. The administration app redirects to Setup while
the API is unconfigured. Choose SQLite for the smallest local installation,
use `data/aginex.db` as its DSN, and enter the initial administrator email and
password. Setup tests the connection, applies all pending Goose migrations,
synchronizes built-in access records, creates the administrator, and publishes
`AGINEX_CONFIG_FILE` only after the initialized application is ready.

On later starts, the API applies pending migrations and synchronizes built-in
access records before `/health/ready` succeeds. There are no separate migrate
or bootstrap CLI commands. Setup has CSRF, origin, and rate-limit protection
but deliberately has no setup token, so expose a first-run instance only on a
trusted local or private network.

The default development endpoints are:

| Service | URL |
|---|---|
| Admin application | <http://localhost:3000> |
| API | <http://localhost:8080/api/v1> |
| Interactive API documentation | <http://localhost:8080/docs> |
| Liveness check | <http://localhost:8080/api/v1/health/live> |
| Readiness check | <http://localhost:8080/api/v1/health/ready> |

Sign in with the administrator credentials entered in Setup.

## Development commands

| Command | Purpose |
|---|---|
| `go run ./cmd/aginex dev` | Run the API and web development servers together |
| `go run ./cmd/aginex doctor` | Check the local toolchain and project structure |
| `go run ./cmd/aginex check` | Run backend tests, Skill validation, frontend checks, tests, and build |
| `go run ./cmd/aginex check --skip-build` | Run the verification gate without the production web build |
| `go run ./cmd/aginex generate client` | Regenerate OpenAPI and the TypeScript API client |
| `go run ./cmd/aginex skills validate` | Validate all canonical Agent Skills |
| `go run ./cmd/worker` | Run the durable PostgreSQL worker |
| `pnpm dev:web` | Run only the Next.js development server |
| `pnpm e2e` | Run Chromium workflows against automatically initialized API/web processes |

Run individual checks when narrowing down a failure:

```bash
go test ./...
go vet ./...
pnpm check:web
pnpm test:web
pnpm build:web
```

The browser gate uses PostgreSQL jobs so it can verify that a file deletion
creates a durable cleanup task. CI provisions that database automatically.
For a local run, select an initially absent `AGINEX_CONFIG_FILE`, enable
PostgreSQL jobs, and provide the Setup form inputs as
`AGINEX_E2E_DATABASE_DSN`, `AGINEX_E2E_ADMIN_EMAIL`, and
`AGINEX_E2E_ADMIN_PASSWORD`. Do not set the runtime `AGINEX_DATABASE_*` or
`AGINEX_BOOTSTRAP_ADMIN_*` variables for this gate: the serial browser workflow
must begin in Setup mode. Install Chromium with
`pnpm exec playwright install chromium`, then run `pnpm e2e`.

## Configuration

Aginex reads runtime configuration from environment variables. The CLI does
not load `.env` automatically, so export it before starting a process. Durable
installation state lives in `AGINEX_CONFIG_FILE`: browser Setup stores its
managed database DSN and session secret there (generating the secret when it is
not supplied), while preconfigured database environments persist only a driver
marker and keep the DSN in the environment. Protect and back up this file.

| Variable | Default | Description |
|---|---|---|
| `AGINEX_ENV` | `development` | Runtime environment |
| `AGINEX_HTTP_ADDRESS` | `:8080` | API listen address |
| `AGINEX_API_PUBLIC_URL` | `http://localhost:8080` | Externally reachable API base URL; workers also probe mode and readiness through it before opening the database |
| `AGINEX_WEB_ORIGINS` | `http://localhost:3000` | Comma-separated browser-origin allowlist |
| `AGINEX_TRUSTED_PROXIES` | empty | Comma-separated proxy IPs or CIDRs whose forwarding headers are trusted; all-address ranges such as `0.0.0.0/0` and `::/0` are rejected |
| `AGINEX_CONFIG_FILE` | `data/aginex-config.json` | Private, durable installation marker and managed Setup configuration; container images use `/data/aginex-config.json` |
| `AGINEX_DATABASE_DRIVER` | empty | Leave this and the DSN empty for browser Setup, or set both for preconfigured startup; `sqlite`, `postgres`, or `mysql` in development/test, PostgreSQL in production |
| `AGINEX_DATABASE_DSN` | empty | Driver-specific connection string; the API credential must apply migrations and synchronize built-in access records |
| `AGINEX_SESSION_SECRET` | generated | Optional session-secret override; generated and persisted on first installation when empty |
| `AGINEX_SESSION_COOKIE` | `aginex_session` | Fixed browser cookie name; overrides are rejected to keep runtime and OpenAPI aligned |
| `AGINEX_SESSION_TTL` | `24h` | Session lifetime as a Go duration |
| `AGINEX_SESSION_SECURE` | `false` | Require HTTPS for the session cookie |
| `AGINEX_SESSION_SAME_SITE` | `lax` | `lax`, `strict`, or `none` |
| `AGINEX_CSRF_COOKIE` | `aginex_csrf` | Fixed double-submit CSRF cookie name |
| `AGINEX_CSRF_HEADER` | `X-CSRF-Token` | Fixed required header documented for browser write operations |
| `AGINEX_IDEMPOTENCY_DRIVER` | `database` | `database` or explicit `disabled` mode |
| `AGINEX_IDEMPOTENCY_TTL` | `24h` | Retention window for safe replay responses |
| `AGINEX_JOBS_DRIVER` | `disabled` | `disabled` or `postgres`; worker and production `FilesModule` compositions require `postgres` |
| `AGINEX_JOBS_WORKER_ID` | `aginex-worker` | Unique identity for each worker replica |
| `AGINEX_BOOTSTRAP_ADMIN_EMAIL` | — | One-time administrator email for an environment-configured first start that bypasses browser Setup |
| `AGINEX_BOOTSTRAP_ADMIN_PASSWORD` | — | Matching one-time administrator password; insecure and whitespace-padded values are rejected in production |
| `AGINEX_STORAGE_DRIVER` | `local` | `local`, `s3`, or `oss` |
| `AGINEX_STORAGE_LOCAL_ROOT` | `data/uploads` | Local object root |
| `AGINEX_STORAGE_BUCKET` | — | Cloud storage bucket |
| `AGINEX_STORAGE_REGION` | — | Cloud storage region |
| `AGINEX_STORAGE_ENDPOINT` | — | S3-compatible or OSS endpoint |
| `AGINEX_STORAGE_ACCESS_KEY_ID` | — | Cloud access key ID |
| `AGINEX_STORAGE_ACCESS_KEY_SECRET` | — | Cloud access key secret |
| `AGINEX_API_INTERNAL_URL` | `http://127.0.0.1:8080` | Server-only API URL used by the Next runtime; inject a reachable service URL such as `http://api:8080` into a separate web container; it is not a browser variable or build argument |
| `NEXT_PUBLIC_API_URL` | empty | Browser-facing API origin compiled into the web build; empty uses same-origin `/api/v1` routing |

See [`.env.example`](.env.example) for HTTP limits, CSRF, rate-limit, job,
idempotency, and storage settings. Production rollout and read-only container
examples are in the [operations guide](docs/operations.md).

### Optional development services

The repository includes Compose profiles for PostgreSQL, MySQL, and MinIO:

```bash
docker compose --profile postgres up -d
docker compose --profile mysql up -d
docker compose --profile storage up -d
```

After starting a database, either enter its connection in browser Setup or set
both database variables plus the one-time administrator values for a
preconfigured start. Storage remains environment-configured. The Compose
credentials are for local development only.

## API conventions

- All application APIs live under `/api/v1`.
- Errors use `application/problem+json`.
- Lists use `{items, page, pageSize, total}`.
- Permissions use lowercase `resource:action` identifiers and are enforced by
  the API, not only hidden in the UI.
- Every successful mutation writes an audit record.
- OpenAPI is generated to [`docs/openapi.json`](docs/openapi.json), and the web
  client is generated to
  [`apps/web/lib/api.generated.ts`](apps/web/lib/api.generated.ts).

## Repository layout

```text
.agents/skills/                     Canonical development workflows
.github/workflows/                  CI verification
apps/web/                           Next.js admin application
cmd/aginex/                         Developer CLI
cmd/openapi/                        OpenAPI generator
cmd/server/                         Go API executable
cmd/worker/                         Durable background worker
docs/                               Product, dependency, and API documentation
framework/                          Reusable module, authorization, job, and HTTP contracts
internal/app/                       HTTP application and business endpoints
internal/auth/                      Authentication and authorization services
internal/platform/migrate/          Goose migrations for all SQL dialects
internal/platform/storage/          Local, S3-compatible, and OSS providers
```

## Project status and roadmap

The current codebase is an executable v1 development baseline. The next major
milestones are:

- OIDC Authorization Code + PKCE and identity linking
- Complete user and role mutations and product editing workflows
- `aginex new` and resource generation
- Browser end-to-end tests and a mandatory live OSS release gate
- Idempotency cleanup scheduling and operator dashboards for durable jobs
- Stable release documentation

See the [v1 product requirements](docs/v1-product-requirements.md) for detailed
scope and acceptance criteria. Existing pre-release applications should also
follow the [breaking remediation upgrade guide](docs/prestable-upgrade.md).

## Documentation

- [Documentation index (中文)](docs/README.md)
- [v1 product requirements (中文)](docs/v1-product-requirements.md)
- [Third-party library decisions (中文)](docs/third-party-libraries.md)
- [Web internationalization](docs/internationalization.md)
- [Production operations](docs/operations.md)
- [Generated OpenAPI contract](docs/openapi.json)

## Agent handoff checklist

Before handing a change back to the requester or opening a pull request:

1. Confirm that you followed every applicable Skill.
2. Confirm that database, permission, audit, and API contracts remain intact.
3. Regenerate OpenAPI and the TypeScript client when API types change.
4. Include tests for changed behavior.
5. Run `go run ./cmd/aginex check` and disclose any check you could not run.

## License

Copyright 2026 Aginex contributors.

Licensed under the [Apache License 2.0](LICENSE).
