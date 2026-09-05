# Aginex Agent Guide

Read the matching Skill under `.agents/skills/` before changing the project.

## Invariants

- Aginex is pre-release. Unless the user explicitly asks for compatibility, prefer one coherent current contract over shims for unshipped configuration, APIs, generated clients, or migrations; update the unpublished baseline directly. Until a release boundary is explicitly declared, installation configuration always reads and writes the single strict version `1`, even when its unpublished fields change; any other version and unknown fields fail closed. Keep one migration family per database dialect. Stale local drafts are handled only by the explicit recoverable `aginex dev reinitialize` workflow, never by runtime compatibility branches.
- Goose SQL migrations are the schema authority; never use `AutoMigrate`.
- Support SQLite, PostgreSQL, and MySQL without dialect logic in services.
- Browser authentication uses revocable server-side sessions.
- Permissions use lowercase `resource:action` identifiers and are enforced by the API.
- Every successful write produces an audit record.
- HTTP APIs live under `/api/v1`, errors use `application/problem+json`, and lists use `{items,page,pageSize,total}`.
- Cloud SDKs stay behind platform interfaces.
- Do not overwrite application-owned or generated files without detecting drift.

## Skill Routes

- New project: `create-aginex-project`
- CRUD/entity/module: `add-business-resource`
- Publish/archive/approve/custom action: `add-custom-api-operation`
- Dashboard/report/settings/UI: `add-admin-page`
- Table/column/index/migration: `change-database-schema`
- Role/permission/access: `configure-rbac`
- Files/images/resumable upload/S3/MinIO/R2/OSS: `add-image-upload`
- Bug/CI/readiness: `test-and-debug`
- Framework/dependency release: `upgrade-aginex`
