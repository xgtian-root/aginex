---
name: test-and-debug
description: Diagnose and verify Aginex backend, frontend, migration, authentication, RBAC, contract, storage, and end-to-end failures using the narrowest reproducible check and a complete final verification gate. Use for bugs, failing CI, broken builds, regressions, or readiness checks.
---

# Test and Debug

## Workflow

1. Reproduce the smallest failure and capture the exact command, status, request ID, and first useful error.
2. Classify it as environment, migration, backend, auth/RBAC, API contract, frontend, storage, or E2E.
3. Read the nearest implementation and test before changing code; preserve unrelated work.
4. Add or strengthen a regression test that fails for the observed reason.
5. Fix the root cause, run the narrow test, then expand verification.

## Verification Order

1. `go -C server test ./...` (and `go -C cli test ./...` in the framework source repository)
2. `pnpm check:admin`
3. `pnpm test:admin`
4. `pnpm build:admin`
5. Database/storage integration and Playwright checks relevant to the change
6. `aginex doctor` and `aginex check`

## Constraints

- Do not delete data, reset a database, or regenerate user files to hide a failure.
- Do not weaken validation, authorization, or tests to make CI green.
- Do not report a provider or dialect as verified when it was skipped.

## Completion Gate

The regression test proves the cause, the narrow fix passes, the full relevant gate passes, and any unavailable external matrix entry is reported explicitly.
