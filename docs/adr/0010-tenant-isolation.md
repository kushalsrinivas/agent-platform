# ADR 0010: Tenant isolation

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [data-ownership.md](../data-ownership.md), [failure-and-recovery.md](../failure-and-recovery.md)

## Context

The platform is multi-tenant. A tenant is an organisation; projects belong to an organisation. Tenant data appears
in PostgreSQL, NATS subjects, object-storage keys, logs, traces and worker memory. Isolation must be enforced
server-side, not by UI filtering, and must remain safe when a service has a bug.

Phase 1 runs shared infrastructure and shared workers. Some future customers will require stronger isolation
(dedicated workers or infrastructure), but that must be a deployment tier, not a different security model.

## Decision

### Identity and authorisation

- OIDC authenticates a principal. Authorisation checks a membership row for the route `org_id`. Roles are
  org-wide in Phase 1: `owner`, `operator`, `viewer`.
- The tenant is always derived from the route + membership; `org_id` in a request body or token claim is not
  trusted. Cross-tenant resource IDs return `404` to avoid existence leaks.
- All tenant-facing API calls are scoped; there is no tenant-agnostic data endpoint except the platform worker
  fleet, which requires a platform-operator role.

### Storage

- Every tenant-owned table has `org_id NOT NULL`; indexes lead with `org_id`. Row-level security is enabled with
  `org_id = current_setting('app.tenant_id', true)`; a missing setting yields zero rows.
- The owning service sets `app.tenant_id` per transaction. The orchestrator's cross-tenant scheduler uses a
  documented `BYPASSRLS` role with an enumerated set of fleet queries; it still sets tenant context for any
  single-tenant operation.
- Object keys include `org=<org_id>` and `project=<project_id>`. Presigned URLs are issued only after verifying the
  caller's/attempt's tenant and are single-object, single-method, 15 minutes.
- NATS event subjects include `org_id`; the API subscribes only to its tenant's subtree and re-filters on envelope
  `tenant_id`. Production NATS credentials restrict subjects.
- Checkpoints, results and logs are tenant data and inherit the same protections. Credentials are encrypted with a
  KMS key and never returned by any read endpoint.

### Execution

- Workers are platform-level components and may run tasks for multiple tenants, one sandbox per attempt. A worker
  receives only the task it is assigned, scoped credentials at attempt start, and no database/broker access.
- A task dispatch includes `tenant_id`; the worker rejects a mismatch. A sandbox cannot enumerate other tenants'
  tasks or objects.
- Dedicated worker pools (labels/tiers) are available as an enterprise isolation option. They are an operational
  boundary, not a code-path change.

### Observability

- Logs and traces carry `org_id`, `execution_id` and `task_id` but never prompt contents or credentials by
  default. Access to observability is restricted to platform operators; tenant-facing debugging uses the API.
- Metrics must not use tenant IDs as unbounded labels; aggregation is by org tier/plan where needed.

## Consequences

**Positive**

- One security model across all storage and messaging layers; RLS provides defense in depth.
- Shared workers keep utilisation high; dedicated pools are an upgrade path, not a rewrite.
- Tenant-scoped subjects and keys make accidental cross-tenant reads visible and preventable.

**Negative**

- Shared workers are inside the trust boundary for tenant task payloads; a compromised worker can read the task it
  is running and potentially exploit the shared kernel (ADR 0011).
- RLS adds query complexity and can hide bugs as "no rows"; tests must distinguish missing tenant context from
  missing data.
- The orchestrator's cross-tenant role bypasses RLS; code review and tests must enforce scoping.
- Per-tenant encryption keys are not implemented; a single KMS key with per-object encryption context is used in
  Phase 1. Key-per-tenant is a future compliance option.

## Alternatives considered

- **Database per tenant.** Strongest isolation, but migration/operations cost and connection explosion for many
  small tenants. Could be a tier later.
- **Schema per tenant.** Middle ground, but still requires routing and migration fan-out; shared schema + RLS is
  adequate for Phase 1 with trusted code review.
- **Application-only filtering.** Simpler, but one forgotten `WHERE` is a breach; RLS adds a second gate.
- **Per-tenant NATS accounts/streams.** Strong, but operationally heavy at tenant counts; subject namespacing plus
  credentials is sufficient initially.
- **Tenant-specific worker clusters by default.** Too expensive and unnecessary for early customers.

## Verification

- Every tenant table test asserts RLS blocks cross-tenant reads and writes without `app.tenant_id`.
- API tests call every endpoint with a foreign ID and assert `404`.
- NATS credentials test proves the API cannot subscribe outside its tenant subtree; worker can consume only its
  dispatch subject.
- Object-storage test proves a presigned URL cannot read another tenant's key.
- A grep/architecture test lists every `BYPASSRLS` query and requires review.
