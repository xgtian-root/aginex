---
name: create-aginex-project
description: Create and initialize an Aginex application with a chosen database and storage provider, secure local administrator, installed dependencies, migrations, and verified API and web builds. Use for new projects, fresh workspaces, starter apps, or initial framework setup.
---

# Create Aginex Project

## Workflow

1. Confirm the target directory is empty or resolve every collision without overwriting.
2. Run `aginex new <name>` with explicit database and storage choices when supplied.
3. Copy `.env.example` to local environment configuration without committing secrets.
4. Set a random session secret and an administrator password of at least 12 characters.
5. Start the selected database and storage services, apply migrations, and create the administrator.
6. Run `aginex doctor`, `go test ./...`, `pnpm check:web`, and a production web build.
7. Report the API, web, OpenAPI, and API documentation URLs plus any optional services not started.

## Constraints

- Do not initialize inside a non-empty directory without resolving collisions.
- Do not commit `.env`, credentials, database files, or uploaded objects.
- Do not claim PostgreSQL/MySQL/OSS readiness based only on SQLite/Local checks.

## Completion Gate

The health endpoint is ready, the administrator can sign in, database migrations are current, and backend and frontend verification pass.
