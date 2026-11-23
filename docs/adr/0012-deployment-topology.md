# ADR 0012: Deployment topology — Docker Compose first, Kubernetes on triggers

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [architecture.md](../architecture.md), [failure-and-recovery.md](../failure-and-recovery.md)

## Context

The team needs a working environment quickly and a production shape that can grow. Kubernetes is the default
answer for many teams, but it brings control-plane overhead, a learning curve for worker state, and a temptation to
split services before their boundaries are proven. Phase 1 has four deployables, a database, a broker and object
storage.

The architecture is already horizontally scalable: stateless API/orchestrator replicas, workers by capacity, CAS
leases and an outbox. The deployment mechanism should not change the domain contracts.

## Decision

### Phase 1

- **Docker Compose** on a single Linux host for development and initial deployment:
  PostgreSQL 16, NATS 2.x with JetStream, MinIO (S3-compatible), `api`, `orchestrator` (1+ replicas), `worker` (1+
  replicas), `dashboard`.
- Binaries are the same across environments; configuration is environment variables and mounted files. No
  environment-specific code paths.
- Migrations run as a one-shot job in dependency order (`control`, then `execution`).
- Health: liveness and readiness endpoints per service; Compose restarts unhealthy containers.
- Backups: PostgreSQL WAL/base backup + object-storage bucket versioning; NATS is not backed up for correctness.
- Production requires a managed PostgreSQL or a synchronous standby before claiming RPO 0. A single-host Compose
  deployment with a local disk does **not** provide HA, and the docs must say so.
- Workers are stateful only in the sense of a stable ID and local sandbox cleanup; they can be recreated.

### Kubernetes (or equivalent orchestrator) trigger

Adopt Kubernetes when at least one is true:

1. worker fleet needs autoscaling or multi-node placement based on capacity;
2. API/orchestrator need rolling deploys with multiple replicas under a real ingress/load balancer;
3. the team needs standard secrets/config rollout, network policies and observability integrations;
4. a managed PostgreSQL/NATS/object-store offering is adopted and the remaining services are simple Deployments.

When adopted, the mapping is straightforward: Deployments for `api`, `orchestrator`, `dashboard`; a StatefulSet or
Node pool for `worker` if it needs stable IDs; Jobs for migrations; PDBs; network policies. The outbox, leases and
reaper remain unchanged. No leader election is required for correctness; only the reaper advisory lock is
singleton.

## Consequences

**Positive**

- Fast local setup and a minimal operational surface while boundaries settle.
- Same artifacts later; no rewrite to containerise further.
- Forced clarity about which state is durable (PostgreSQL/object storage), because Compose gives no magic.

**Negative**

- Compose on one host is not highly available; a host failure is an outage. This is acceptable only for development
  and early single-tenant deployments.
- Manual capacity management for workers.
- No native network policies/service mesh; security relies on host firewalling and mTLS.
- A Kubernetes migration still requires effort (ingress, secrets, storage classes, probes), even if code is ready.

## Alternatives considered

- **Kubernetes from day one.** Better long-term operations, but heavy for four services and a small team; encourages
  premature service splitting and distracts from state-machine correctness.
- **Nomad.** Lighter than Kubernetes and good for mixed workloads, but a smaller ecosystem and another bespoke
  operational skill; not needed for Phase 1.
- **Serverless functions.** Poor fit for long-lived gRPC streams, worker sandboxes and lease heartbeats.
- **Bare systemd units.** Simple, but no scheduling, service discovery or rolling deploy; Compose gives a better
  developer story.

## Verification

- The same container images run locally and in the single-host deployment; config diff is environment-only.
- A rolling restart test proves no committed state is lost and workers re-register/recover leases.
- A host-failure exercise documents the actual RTO/RPO; no claim of HA is made for Compose.
- The Kubernetes trigger is reviewed when worker autoscaling or multi-node placement is requested.
