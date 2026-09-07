---
name: test-and-debug
description: Diagnose and verify Aginex backend, frontend, migration, authentication, RBAC, contract, storage, and end-to-end failures using the narrowest reproducible check and a complete final verification gate. Use for bugs, failing CI, broken builds, regressions, or readiness checks.
---

# Test and Debug

## Workflow

1. Reproduce the smallest failure and capture the exact command, status, request ID, and first useful error.
2. Classify it as environment, migration, backend, auth/RBAC, API contract, frontend, storage, or E2E.
3. Read the nearest implementation and test before changing code; preserve unrelated work.
4. Add or strengthen a regression test when a behavior defect or data/auth risk warrants it; use existing focused checks for documentation, configuration, or low-impact changes.
5. Fix the root cause, run the narrow test, then expand verification.

## Verification by Change

Start with the smallest check that exercises the changed behavior. Expand when affected contracts, shared infrastructure, failures, or an explicit CI/release gate justify it.

| Change | Relevant verification |
| --- | --- |
| Backend Go | Affected packages/tests in `server`; use `go -C server test ./...` for broad backend changes or the full backend gate. |
| CLI/framework tooling | Affected checks in `cli`; use `go -C cli test ./...` for broad CLI changes. |
| Admin UI | Relevant UI tests and `pnpm check:admin`; include `pnpm test:admin` and `pnpm build:admin` when the affected frontend/build surface requires them. |
| Database schema | Follow `change-database-schema`: review all three dialects and run available disposable-database migration checks. |
| Auth, storage, API contracts | Exercise the affected boundary and consumers; run the relevant integration/Playwright matrix, including browser behavior when applicable. |
| Installation or generated projects | Relevant `aginex doctor` / `aginex check` and scaffold verification. |
| Documentation/instructions only | Check semantics, links, and affected template synchronization; application suites are not a default prerequisite. |

## Constraints

- Do not delete data, reset a database, or regenerate user files to hide a failure.
- Do not weaken validation, authorization, or tests to make CI green.
- Do not report a provider or dialect as verified when it was skipped.

## Completion Gate

The evidence demonstrates the cause, the scoped fix and required relevant gates pass, and any unavailable external matrix entry is reported explicitly.
