# Findings

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

## Local Toolchain

- Go 1.25.3 on darwin/arm64.
- Node.js 22.19.0 and pnpm 10.33.2.
- Docker 29.4.0.
- Git 2.50.1; the workspace has not yet been initialized as a repository.
