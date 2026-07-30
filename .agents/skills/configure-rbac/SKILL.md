---
name: configure-rbac
description: Add or modify Aginex role-based access control using explicit resource:action permission records, backend enforcement, seeded role grants, permission-aware navigation and actions, and forbidden-path tests. Use for access policies, roles, permissions, or authorization changes.
---

# Configure RBAC

## Workflow

1. Name permissions as lowercase `resource:action`; use a narrow action instead of reusing an overly broad grant.
2. Add the permission description to the built-in registry and seed it idempotently.
3. Enforce the permission at the API boundary and again in service logic when operations can be called outside HTTP.
4. Hide navigation and actions that the current principal cannot use; the API remains the authority.
5. Audit role or permission changes.
6. Test unauthenticated (401), authenticated forbidden (403), and granted success paths.

## Constraints

- Do not implement authorization only in the frontend.
- Do not introduce dynamic menu records, ABAC, tenant scope, or Casbin in v1.
- Do not silently grant a new permission to non-administrator roles.

## Completion Gate

Permission naming is consistent, seed behavior is idempotent, API and UI agree, denied paths leak no protected data, and tests cover all three authorization outcomes.
