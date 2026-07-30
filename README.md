# Aginex

Aginex is an Apache-2.0 admin framework built with Go, Gin, and Next.js. It is
designed for both human maintainers and Coding Agents: project workflows are
documented as portable Agent Skills and guarded by executable contracts.

## Status

Aginex is an executable v1 development baseline. Local authentication, explicit
RBAC, audit logs, a reference product resource, provider-neutral image uploads,
the responsive admin shell, contract generation, and canonical Agent Skills are
implemented. OIDC, full management mutations, provider integration environments,
the project scaffolder, and release packaging remain on the v1 roadmap.

## Development quick start

```bash
cp .env.example .env
set -a
source .env
set +a
pnpm install
go run ./cmd/aginex dev
```

Open [the web app](http://localhost:3000), sign in with the administrator from
`.env`, and inspect [API documentation](http://localhost:8080/docs).

Useful commands:

```bash
go run ./cmd/aginex doctor
go run ./cmd/aginex skills validate
go run ./cmd/aginex generate client
go run ./cmd/aginex check
```

The implementation targets:

- Go 1.25+ and Gin
- Next.js 16 Active LTS
- PostgreSQL, MySQL, and SQLite
- Local, S3-compatible, and Alibaba Cloud OSS storage
- Server-side sessions, explicit RBAC, and audit logs
- Portable Agent Skills for repeatable development workflows

## Verification

```bash
go test ./...
pnpm check:web
pnpm test:web
pnpm build:web
```

PostgreSQL and MySQL migration integration tests run when
`AGINEX_TEST_POSTGRES_DSN` and `AGINEX_TEST_MYSQL_DSN` are set. CI supplies both.

## Repository layout

```text
apps/web/       Next.js admin application
cmd/server/     Go API executable
cmd/aginex/     Aginex CLI
internal/       Backend platform and business modules
db/migrations/  Explicit database migrations by dialect
docs/           Product and technical documentation
.agents/skills/ Canonical project Agent Skills
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
