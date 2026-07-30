# Aginex v1 Implementation Plan

## Goal

Build the first production-capable vertical slice of Aginex: an Apache-2.0,
Coding-Agent-first admin framework based on Go/Gin and Next.js, with
machine-verifiable contracts and Skill-based development documentation.

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
