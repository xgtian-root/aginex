---
name: create-aginex-project
description: Create and initialize an Aginex application with a chosen database and storage provider, secure local administrator, installed dependencies, migrations, and verified API and web builds. Use for new projects, fresh workspaces, starter apps, or initial framework setup.
---

# Create Aginex Project

## Workflow

1. Confirm the target directory is empty or resolve every collision without overwriting.
2. Run `aginex new` to initialize the empty current directory, or `aginex new <name>` to create a new child directory. Supply `--module <path>` when the publishable Go module path is known. For an unpublished source-built CLI only, explicitly supply `--aginex-path <checkout>` and treat its local `replace` directive as development-only.
3. Copy `.env.example` to local environment configuration without committing secrets; project creation itself never writes `.env` or credentials.
4. Set a cryptographically random session secret and a strong, non-empty administrator password.
5. Start the selected database and storage services, apply migrations, and create the administrator.
6. Run `aginex doctor`, `go -C backend test ./...` (and `go -C cli test ./...` in the framework source repository), `pnpm check:admin`, and a production web build.
7. Report the API, web, OpenAPI, and API documentation URLs plus any optional services not started.

## Constraints

- Do not initialize inside a non-empty directory without resolving collisions.
- Do not commit `.env`, credentials, database files, or uploaded objects.
- Do not claim PostgreSQL/MySQL/OSS readiness based only on SQLite/Local checks.

## Completion Gate

The health endpoint is ready, the administrator can sign in, database migrations are current, and backend and frontend verification pass.
