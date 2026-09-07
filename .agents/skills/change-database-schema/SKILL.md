---
name: change-database-schema
description: Evolve an Aginex database schema safely across SQLite, PostgreSQL, and MySQL with Goose up/down migrations, compatible Go and API changes, data backfills, and migration verification. Use when adding, changing, renaming, indexing, or removing persisted fields or tables.
---

# Change Database Schema

## Workflow

1. Inspect the affected schema, dialect migrations, release boundary, data assumptions, and API/UI contracts.
2. For Aginex's unpublished pre-release baseline, update the existing baseline coherently in `sqlite`, `postgres`, and `mysql`. Do not add compatibility shims or a second migration family for unshipped drafts unless the user explicitly requests compatibility.
3. For a released migration or a deployed consumer with existing data, leave released SQL immutable and add equivalent versioned Goose up/down migrations in all three dialects. Use expand-and-contract or an explicit migration/backfill strategy when needed to preserve data and contracts.
4. Update affected models and DTOs; keep dialect SQL out of services and handlers.
5. Verify equivalent up/down intent across all dialects, then exercise the changed migration and affected application behavior on disposable databases for available dialects. Report unavailable dialects explicitly.

## Constraints

- Never use GORM `AutoMigrate` or edit an already released migration.
- Preserve the strict configuration version and pre-release contract in `AGENTS.md`.
- Stale local drafts use the explicit recoverable `aginex dev reinitialize` workflow; do not reset data or invoke it just to make a test pass.
- Destructive or narrowing changes to existing user data require an explicit data-preservation or approved loss plan.

## Completion Gate

All three dialects express equivalent schema intent, relevant migration/application checks pass, released data has a safe path, and contract changes plus unavailable verification are documented. Sync changed canonical scaffold assets through the template generator without importing unrelated drift.
