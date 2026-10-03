---
name: create-aginex-project
description: Create and initialize an Aginex application with a chosen database and storage provider, secure local administrator, installed dependencies, migrations, and verified API and web builds. Use for new projects, fresh workspaces, starter apps, or initial framework setup.
---

# Create Aginex Project

## Workflow

1. For current-directory initialization, confirm neither an `admin` nor a `server` path exists. Existing unrelated content is preserved; scaffold conflicts are automatically backed up before replacement.
2. Run `aginex-cli new` to initialize the current directory, or `aginex-cli new <name>` to create a new child directory. Supply `--module <path>` when the publishable Go module path is known. For an unpublished source-built CLI only, explicitly supply `--aginex-path <checkout>` and treat its local `replace` directive as development-only.
3. Copy `server/.env.example` to `server/.env` and `admin/.env.example` to `admin/.env` when local configuration is needed; both services load their own file, with process environment taking precedence. Use `aginex-cli config` to inspect or update startup settings without committing secrets; project creation itself never writes `.env` or credentials.
4. Set a cryptographically random session secret and a strong, non-empty administrator password.
5. Start the selected database and storage services, apply migrations, and create the administrator.
6. Run `aginex-cli doctor`, `go -C server test ./...` (and `go -C cli test ./...` in the framework source repository), `pnpm check:admin`, and a production web build.
7. Report the API, web, OpenAPI, and API documentation URLs plus any optional services not started.

## Constraints

- Do not bypass the reserved `admin`/`server` path checks. Review the reported `.aginex-backup-<timestamp>-<random>/` directory after initialization; successful backups remain until explicitly removed. Named targets must not already exist.
- Do not commit `.env`, credentials, database files, or uploaded objects.
- Do not claim PostgreSQL/MySQL/OSS readiness based only on SQLite/Local checks.

## Completion Gate

The health endpoint is ready, the administrator can sign in, database migrations are current, and backend and frontend verification pass.
