# Aginex Agent Guide

Read the matching Skill under `.agents/skills/` before changing the project.

## Invariants

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
- Image/S3/MinIO/R2/OSS: `add-image-upload`
- Bug/CI/readiness: `test-and-debug`
- Framework/dependency release: `upgrade-aginex`
