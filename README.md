# Aginex

<p align="center">
  <strong>An agent-first admin framework for Go and Next.js.</strong>
</p>

<p align="center">
  Build auditable business applications with explicit contracts for developers,
  coding agents, and CI.
</p>

<p align="center">
  <a href="https://github.com/xgtian-root/aginex/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xgtian-root/aginex/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/xgtian-root/aginex/blob/main/LICENSE"><img alt="Apache-2.0 license" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Node.js 22" src="https://img.shields.io/badge/Node.js-22-5FA04E?logo=nodedotjs&logoColor=white">
</p>

> [!IMPORTANT]
> Aginex is currently an early development baseline, not a stable release.
> Authentication, RBAC, audit logs, product CRUD, image uploads, the admin
> shell, contract generation, and Agent Skills are implemented. APIs and
> project conventions may still change before v1.

## Why Aginex?

Most admin frameworks document the final code but leave the development process
implicit. Aginex treats that process as part of the product. Its architecture,
permissions, API contracts, migrations, verification gates, and common change
workflows are explicit and machine-checkable.

This gives human maintainers and coding agents the same guardrails:

- **One typed stack:** Go and Gin on the backend, Next.js and React on the
  frontend, with an OpenAPI-generated TypeScript contract between them.
- **Security by construction:** revocable server-side sessions, API-enforced
  `resource:action` permissions, origin checks, and an audit event for every
  successful write.
- **Portable infrastructure:** SQLite, PostgreSQL, and MySQL support; Local,
  S3-compatible, and Alibaba Cloud OSS storage behind common interfaces.
- **Reviewable evolution:** Goose migrations are the schema authority and
  generated contracts are checked for drift in CI.
- **Agent-ready workflows:** nine portable Agent Skills describe how to add
  resources, pages, custom actions, migrations, permissions, uploads, and more.

## What is included?

| Area | Current baseline |
|---|---|
| Identity | Local administrator bootstrap, Argon2id passwords, revocable cookie sessions |
| Authorization | Explicit RBAC with lowercase `resource:action` permissions |
| Audit | Actor, action, resource, request, and timestamp records |
| Business example | Searchable, paginated product CRUD |
| Files | Upload intent, direct upload, verification, signed reads, and deletion |
| Admin UI | Login, dashboard, products, users, roles, audit, and files pages |
| API contract | `/api/v1`, RFC 9457-style problem responses, OpenAPI, generated TypeScript client |
| Tooling | `dev`, `doctor`, `check`, contract generation, and Skill validation commands |

## Architecture

```text
Browser
   │
   ├── Next.js admin application ────────┐
   │                                     │ generated TypeScript client
   └──────────────────────────────► Go API (/api/v1)
                                           │
                              ┌────────────┼────────────┐
                              │            │            │
                         SQL database   Audit log   Object storage
                       SQLite/Postgres/            Local/S3/OSS
                            MySQL
```

The service layer stays database-dialect neutral. Schema differences live in
explicit Goose migrations, while cloud-specific SDKs stay behind storage
interfaces.

## Quick start

### Prerequisites

- Go 1.25 or newer
- Node.js 22
- pnpm 10.33.2
- Docker (optional, for PostgreSQL, MySQL, or MinIO)

### 1. Clone and configure

```bash
git clone https://github.com/xgtian-root/aginex.git
cd aginex
cp .env.example .env
```

Before the first run, edit `.env` and replace these development credentials:

```dotenv
AGINEX_SESSION_SECRET=replace-with-at-least-32-random-bytes
AGINEX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
AGINEX_BOOTSTRAP_ADMIN_PASSWORD=change-me-before-production
```

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

The default development endpoints are:

| Service | URL |
|---|---|
| Admin application | <http://localhost:3000> |
| API | <http://localhost:8080/api/v1> |
| Interactive API documentation | <http://localhost:8080/docs> |
| Health check | <http://localhost:8080/healthz> |

Sign in with `AGINEX_BOOTSTRAP_ADMIN_EMAIL` and
`AGINEX_BOOTSTRAP_ADMIN_PASSWORD`. The administrator is created on the first
successful bootstrap.

## Development commands

| Command | Purpose |
|---|---|
| `go run ./cmd/aginex dev` | Run the API and web development servers together |
| `go run ./cmd/aginex doctor` | Check the local toolchain and project structure |
| `go run ./cmd/aginex check` | Run backend tests, Skill validation, frontend checks, tests, and build |
| `go run ./cmd/aginex check --skip-build` | Run the verification gate without the production web build |
| `go run ./cmd/aginex generate client` | Regenerate OpenAPI and the TypeScript API client |
| `go run ./cmd/aginex skills validate` | Validate all canonical Agent Skills |
| `pnpm dev:web` | Run only the Next.js development server |

Run individual checks when narrowing down a failure:

```bash
go test ./...
go vet ./...
pnpm check:web
pnpm test:web
pnpm build:web
```

## Configuration

Aginex reads configuration from environment variables. The CLI does not load
`.env` automatically, so export it before starting a process.

| Variable | Default | Description |
|---|---|---|
| `AGINEX_ENV` | `development` | Runtime environment |
| `AGINEX_HTTP_ADDRESS` | `:8080` | API listen address |
| `AGINEX_API_PUBLIC_URL` | `http://localhost:8080` | Externally reachable API base URL |
| `AGINEX_WEB_ORIGIN` | `http://localhost:3000` | Allowed browser origin |
| `AGINEX_DATABASE_DRIVER` | `sqlite` | `sqlite`, `postgres`, or `mysql` |
| `AGINEX_DATABASE_DSN` | `data/aginex.db` | Driver-specific connection string |
| `AGINEX_SESSION_SECRET` | — | Session secret; at least 32 bytes in production |
| `AGINEX_SESSION_COOKIE` | `aginex_session` | Browser cookie name |
| `AGINEX_SESSION_TTL` | `24h` | Session lifetime as a Go duration |
| `AGINEX_SESSION_SECURE` | `false` | Require HTTPS for the session cookie |
| `AGINEX_BOOTSTRAP_ADMIN_EMAIL` | — | Initial local administrator email |
| `AGINEX_BOOTSTRAP_ADMIN_PASSWORD` | — | Initial local administrator password |
| `AGINEX_STORAGE_DRIVER` | `local` | `local`, `s3`, or `oss` |
| `AGINEX_STORAGE_LOCAL_ROOT` | `data/uploads` | Local object root |
| `AGINEX_STORAGE_BUCKET` | — | Cloud storage bucket |
| `AGINEX_STORAGE_REGION` | — | Cloud storage region |
| `AGINEX_STORAGE_ENDPOINT` | — | S3-compatible or OSS endpoint |
| `AGINEX_STORAGE_ACCESS_KEY_ID` | — | Cloud access key ID |
| `AGINEX_STORAGE_ACCESS_KEY_SECRET` | — | Cloud access key secret |
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080/api/v1` | Browser-facing API URL |

### Optional development services

The repository includes Compose profiles for PostgreSQL, MySQL, and MinIO:

```bash
docker compose --profile postgres up -d
docker compose --profile mysql up -d
docker compose --profile storage up -d
```

After starting a service, update the matching database or storage variables in
`.env`. The Compose credentials are for local development only.

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

## Agent Skills

`AGENTS.md` routes common development tasks to the canonical Skills in
`.agents/skills/`:

| Task | Skill |
|---|---|
| Create a new Aginex project | `create-aginex-project` |
| Add a CRUD business resource | `add-business-resource` |
| Add publish, archive, approve, or another custom action | `add-custom-api-operation` |
| Add a dashboard, report, settings, or other admin page | `add-admin-page` |
| Change a table, column, index, or data migration | `change-database-schema` |
| Add or change roles and permissions | `configure-rbac` |
| Add an image upload flow or storage provider | `add-image-upload` |
| Diagnose a bug, CI failure, or readiness issue | `test-and-debug` |
| Upgrade framework dependencies and conventions | `upgrade-aginex` |

These Skills encode project invariants, implementation order, required tests,
and completion criteria so that changes remain consistent across different
coding agents.

## Repository layout

```text
.agents/skills/                     Canonical development workflows
.github/workflows/                  CI verification
apps/web/                           Next.js admin application
cmd/aginex/                         Developer CLI
cmd/openapi/                        OpenAPI generator
cmd/server/                         Go API executable
docs/                               Product, dependency, and API documentation
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
- Browser end-to-end tests and live storage provider contract tests
- Release packaging, security scans, and stable public documentation

See the [v1 product requirements](docs/v1-product-requirements.md) for detailed
scope and acceptance criteria.

## Documentation

- [Documentation index (中文)](docs/README.md)
- [v1 product requirements (中文)](docs/v1-product-requirements.md)
- [Third-party library decisions (中文)](docs/third-party-libraries.md)
- [Generated OpenAPI contract](docs/openapi.json)

## Contributing

Issues and pull requests are welcome. Before opening a pull request:

1. Read `AGENTS.md` and the Skill that matches your change.
2. Keep database, permission, audit, and API invariants intact.
3. Regenerate contracts when API types change.
4. Run `go run ./cmd/aginex check`.

Please keep changes focused and include tests for behavior changes.

## License

Copyright 2026 Aginex contributors.

Licensed under the [Apache License 2.0](LICENSE).
