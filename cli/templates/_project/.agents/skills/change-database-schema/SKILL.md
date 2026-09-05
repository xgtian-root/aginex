---
name: change-database-schema
description: Evolve an Aginex database schema safely across SQLite, PostgreSQL, and MySQL with Goose up/down migrations, compatible Go and API changes, data backfills, and migration verification. Use when adding, changing, renaming, indexing, or removing persisted fields or tables.
---

# Change Database Schema

## Workflow

1. Inspect all existing dialect migrations, data volume assumptions, and affected API/UI contracts.
2. Create the same timestamped/versioned migration in `sqlite`, `postgres`, and `mysql`.
3. Prefer expand-and-contract changes: add nullable/defaulted structure, backfill, switch code, then tighten or remove later.
4. Update models and stable DTOs deliberately; preserve public field names unless the change is explicitly breaking.
5. Test migration up, application behavior, and down on a disposable database for every available dialect.
6. Document irreversible data loss or operational sequencing.

## Constraints

- Never use GORM `AutoMigrate`.
- Never edit an already released migration.
- Never remove or narrow data in one step without an explicit compatibility plan.
- Keep dialect-specific SQL out of services and handlers.

## Completion Gate

All dialects have equivalent up/down intent, application tests pass, existing data has a safe path, and contract changes are documented.
