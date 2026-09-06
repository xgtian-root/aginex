---
name: add-business-resource
description: Add a complete Aginex business resource spanning database migrations, Go domain and HTTP code, resource:action permissions, audit events, Next.js UI, tests, and contract verification. Use when a user asks for CRUD, a new entity, a management module, or an admin-managed data type.
---

# Add Business Resource

Build the smallest complete vertical slice. Follow `products` as the reference implementation.

## Workflow

1. Read `AGENTS.md`, the existing domain models, all three migration directories, backend routes, and the closest web resource.
2. Define fields, invariants, lifecycle states, searchable columns, and the exact permission set before editing.
3. Add matching up/down migrations for SQLite, PostgreSQL, and MySQL. Keep dialect differences inside migrations.
4. Add the model, repository/service behavior, typed HTTP inputs and outputs, pagination, validation, and `application/problem+json` errors.
5. Register `resource:create|read|update|delete` permissions and protect every operation.
6. Audit every successful write with actor, action, resource ID, summary, IP, and request ID.
7. Add the permission-aware Next.js list and form experience. Include loading, error, empty, keyboard-focus, narrow-screen, and destructive states.
8. Add a backend integration test that signs in and exercises the resource. Add frontend tests when behavior is not covered by types.
9. Run `aginex check` or the commands in the completion gate.

## Constraints

- Never use `AutoMigrate`; Goose SQL is the schema authority.
- Never expose GORM models accidentally when a stable response DTO is needed.
- Never add a route without permission enforcement or a write without an audit record.
- Never edit only one database dialect.
- Never overwrite unrelated user changes or generated contracts manually.

## Completion Gate

- `go -C server test ./...` (and `go -C cli test ./...` in the framework source repository)
- `pnpm check:admin`
- `pnpm build:admin`
- Up and down migrations exist for all three dialects.
- API errors, list envelopes, UTC times, permissions, audit events, and responsive UI follow project contracts.
