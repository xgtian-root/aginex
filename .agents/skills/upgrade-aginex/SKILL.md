---
name: upgrade-aginex
description: Upgrade Aginex framework dependencies and conventions while preserving application-owned code, applying migration guidance, checking generated contract drift, and validating backend, frontend, database, and Agent Skills compatibility. Use for framework upgrades, dependency updates, release migrations, or deprecation cleanup.
---

# Upgrade Aginex

## Workflow

1. Record the current version, dirty files, database dialects, storage providers, and local customizations.
2. Read every intermediate release note and migration guide; identify breaking API, config, schema, generated, and Skill changes.
3. Upgrade one compatibility boundary at a time: Go modules, web packages, database migrations, generated client, then project Skills.
4. Never overwrite modified application files. Present conflicts with framework and application intent separately.
5. Run `aginex doctor`, migrations on disposable copies, contract drift checks, backend tests, frontend checks/build, and relevant E2E.
6. Summarize changed defaults, manual steps, rollback point, and skipped provider checks.

## Constraints

- Do not combine framework upgrades with unrelated feature refactors.
- Do not rewrite released migrations or force dependency major versions silently.
- Do not claim success while generated files, Skills, or database versions drift.

## Completion Gate

The project builds and tests, migrations are reversible, generated contracts are clean, custom code remains intact, and rollback instructions are known.
