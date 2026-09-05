---
name: add-custom-api-operation
description: Add a non-CRUD Aginex API operation with typed inputs and outputs, service-layer invariants, explicit permission enforcement, audit coverage, OpenAPI visibility, and tests. Use for actions such as publish, archive, approve, retry, export, or bulk state transitions.
---

# Add Custom API Operation

## Workflow

1. Locate the owning resource and state invariants; choose an action-oriented route and permission such as `products:publish`.
2. Define typed input/output contracts and expected RFC 9457-style problem responses.
3. Put business rules in the service layer and use a transaction when state and audit data must change together.
4. Register the permission, protect the route, and write an audit event after success.
5. Add the UI action only for authorized users, with explicit loading, success, failure, and destructive wording.
6. Test success, invalid transition, missing resource, unauthenticated, and forbidden cases.

## Constraints

- Do not hide business transitions inside generic update handlers.
- Do not use GET for state changes.
- Do not return success before the durable state change completes.

## Completion Gate

The operation appears in OpenAPI, preserves resource invariants, is permission-protected and audited, and passes backend/frontend checks.
