---
name: configure-rbac
description: Add or modify Aginex role-based access control using explicit resource:action permission records, backend enforcement, seeded role grants, permission-aware navigation and actions, and forbidden-path tests. Use for access policies, roles, permissions, or authorization changes.
---

# Configure RBAC

## Workflow

1. Name permissions as lowercase `resource:action`; use a narrow action instead of reusing an overly broad grant.
2. Declare the permission description in the owning module's compiled registry
   and synchronize it idempotently at startup; do not add runtime permission CRUD.
3. Enforce the permission at the API boundary and again in service logic when operations can be called outside HTTP.
4. Hide navigation and actions that the current principal cannot use; the API remains the authority.
5. Keep the system-managed `Administrator` role immutable and synchronized with
   every registered permission at `all` scope.
6. For user, role, or grant mutations, prevent last-administrator removal,
   self-lockout, and delegation beyond the actor's own permissions and scopes.
7. Audit role or permission changes in the same transaction as the mutation and
   revoke affected sessions when access is reduced.
8. Test unauthenticated (401), authenticated forbidden (403), and granted success paths.

## Constraints

- Do not implement authorization only in the frontend.
- Do not introduce dynamic menu records, ABAC, tenant scope, or Casbin in v1.
- Do not silently grant a new permission to non-administrator roles.
- Do not rename, delete, or reduce grants on the `Administrator` role.
- Do not expose public self-registration through access administration.
- Do not allow an actor to grant a permission or scope the actor does not hold.

## Completion Gate

Permission naming is consistent, startup synchronization is idempotent, the
`Administrator` role has exactly all registered grants at `all` scope, API and UI
agree, denied paths leak no protected data, and tests cover 401, 403, successful
writes, last-administrator protection, self-lockout, and the delegation ceiling.
