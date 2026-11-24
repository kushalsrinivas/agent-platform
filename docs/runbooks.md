# Operational runbooks

Use these with `docs/deployment-kubernetes.md` and the dashboards under `infrastructure/monitoring/grafana/`.
Every alert links to a section. Commands assume the Helm release `agent-platform` in namespace
`agent-platform`; the Compose equivalents are shown where useful.

## First response

1. Check `kubectl -n agent-platform get pods` and `kubectl -n agent-platform get events --sort-by=.lastTimestamp | tail`.
2. Check the platform dashboard: API 5xx, queue depth/age, worker fleet, database pool, provider errors.
3. Read the alert's `for` duration; a short spike is not an outage.
4. Record the start time, affected tenants and the first failing component in the incident channel.
5. Do not delete executions, checkpoints, outbox rows or audit events to "clear" an alert.

## 1. Orchestrator outage

**Detect:** `AgentPlatformOrchestratorUnavailable`, `/readyz` failing, executions staying `pending`, no dispatch
metric increase.

```bash
kubectl -n agent-platform rollout status deploy/agent-platform-orchestrator
kubectl -n agent-platform logs deploy/agent-platform-orchestrator --tail=200
kubectl -n agent-platform get pods -l app.kubernetes.io/component=orchestrator
```

- If one replica is unhealthy, let the rolling update or deployment controller replace it; two or more
  orchestrators are safe because dispatch, reaping and outbox claims are row-locked.
- If all replicas are down after a bad release, `helm rollback agent-platform` and re-check `/readyz`.
- Confirm recovery by watching `agent_platform_orchestrator_dispatches_total` increase and the oldest pending
  execution age fall. Queued work is durable; do not resubmit it.
- If the database or NATS is the cause, follow those runbooks instead.

## 2. NATS outage

**Detect:** `AgentPlatformNatsDisconnected`, consumer lag, `agent_platform_nats_connected == 0`, dispatch stops.

```bash
kubectl -n agent-platform get pods -l app.kubernetes.io/name=nats
kubectl -n agent-platform logs nats-0 --tail=200
kubectl -n agent-platform exec nats-0 -- nats-server --signal ldm
```

- With three JetStream nodes, lose one at a time; verify the stream/consumer state before restarting another.
- If the whole cluster is down, restore the NATS PVCs first. Keep the orchestrator running: outbox rows remain
  unpublished and will drain after reconnect.
- Never recreate a JetStream volume without confirming the outbox and PostgreSQL remain the source of truth for
  executions. Workers do not own durable state.
- Verify: `agent_platform_nats_connected` returns to 1 on API/orchestrator/worker, and the queue age falls.

## 3. Database outage

**Detect:** `AgentPlatformDatabaseConnectionsExhausted` or readiness failing on API/orchestrator; executions
stuck; `pg_isready` failing.

```bash
kubectl -n agent-platform exec deploy/agent-platform-api -- wget -qO- localhost:8080/readyz
psql "$POSTGRES_DATABASE_URL" -c "select 1"   # or the managed database console
```

- Fail over to the managed standby and update the secret connection URLs; restart API and orchestrator.
- If the primary is unavailable, stop accepting writes at the ingress rather than letting retries write partial
  work. Execution state is durable in PostgreSQL, so no in-memory state is lost.
- On `too many clients`, check `pg_stat_activity` by role and application; recycle leaked pools by rolling the
  affected deployment, then fix the pool sizing.
- Verify: `/readyz` passes, in-flight attempts either complete or are reclaimed after the lease timeout, and
  `agent_platform_db_connections` is below the maximum.

## 4. Worker fleet failure

**Detect:** `AgentPlatformWorkerFleetUnhealthy`, queue age rising, no worker heartbeat, attempts expiring.

```bash
kubectl -n agent-platform get pods -l app.kubernetes.io/component=worker
kubectl -n agent-platform logs deploy/agent-platform-worker --tail=200
kubectl -n agent-platform get scaledobject
```

- Restart failed worker pods one at a time. KEDA should replace capacity from queue depth; if it does not,
  check the Prometheus trigger and CRDs.
- If workers crash-loop on runtime startup, check the runtime command, memory/CPU limits and the sandbox
  `/tmp` mounts. Lease expiry returns work to the queue; no manual task reset is needed.
- If a single worker is poisoned by a task, drain it (`kubectl scale` down one replica) and let the lease expire.
- Verify: workers register, `agent_platform_worker_slots_total` is non-zero, and pending queue age falls.

## 5. Queue backlog

**Detect:** `AgentPlatformQueueBacklog` or `AgentPlatformQueueAge`, dashboard queue depth/age.

1. Check that the backlog is real work, not a stuck dependency (`execution.execution_tasks.state='pending'`
   with an upstream task not terminal).
2. Check worker capacity and provider limits. Raise KEDA `maxReplicaCount` only if workers are healthy and the
   bottleneck is slots, not provider quota.
3. If a single tenant dominates, lower its quota or raise global fairness; do not delete its executions.
4. If the backlog is caused by a provider outage, follow runbook 6; scaling workers will not help.
5. Verify: oldest pending age and queue depth trend down for 15 minutes.

## 6. Provider outage

**Detect:** `AgentPlatformProviderErrors`, `AgentPlatformProviderRateLimit`, retry spikes, attempts failing with
`provider_unavailable`, `rate_limited` or `timeout`.

```bash
kubectl -n agent-platform logs deploy/agent-platform-worker --tail=200 | grep -i provider
```

- Confirm the provider status page and the configured credential/secret version. Rotate the credential if it
  was revoked.
- Keep retries bounded: check `ORCHESTRATOR_PROVIDER_CONCURRENCY` and `ORCHESTRATOR_PROVIDER_RATE_PER_MINUTE`.
  Lower them if the provider is rate limiting; raise them only after provider confirmation.
- Do not increase retry counts for side-effecting tools. Read-only agents retry safely; non-idempotent tasks
  fail by design when the effect is uncertain.
- Verify: provider error rate returns below the alert threshold and executions complete with retries, not
  duplicate side effects.

## 7. Corrupt execution or checkpoint

**Detect:** `CHECKPOINT_INVALID`, `RESULT_INVALID`, repeated execution failure, replay mismatch.

```bash
psql "$POSTGRES_DATABASE_URL" -c \
  "select id,state,failure_code,failure_detail from execution.executions where id='<id>';"
```

- Do not edit checkpoints or results in place. A corrupt checkpoint is a permanent failure; the execution must
  fail safely, never resume from an older unverified checkpoint.
- Use the replay API (`POST .../executions/{id}:replay`) to create a new execution from the original input, or
  cancel and resubmit with a new idempotency key.
- Preserve the failed execution and its events for audit. If the corruption came from a runtime version, pin the
  previous agent/runtime version before replaying.
- Verify: the replacement execution succeeds and the original remains failed/cancelled with its audit trail.

## 8. Failed deployment

**Detect:** Helm upgrade failed, pods crash-loop, migration hook failed, readiness never passes.

```bash
helm -n agent-platform history agent-platform
helm -n agent-platform status agent-platform
kubectl -n agent-platform get jobs | grep migrate
```

1. If the release is not serving, `helm rollback agent-platform <previous-revision>`.
2. If the migration hook failed, inspect its logs; do not rerun a partially applied migration blindly. Restore
   the pre-upgrade backup before retrying if the schema is inconsistent.
3. Keep the previous image digests available; never deploy `latest`.
4. Verify: pods become ready, migrations show the expected version, and a smoke execution succeeds.
5. Record the failed image/tag and the rollback revision in the incident.

## 9. Secret rotation

**Detect:** planned rotation, credential revocation, `SECRETS_ACTIVE_KEY_ID` change.

1. Generate a new 32-byte key, base64-encode it, and add it as a new key ID in the external secret.
2. Set `SECRETS_ACTIVE_KEY_ID` to the new key and keep the old key in `SECRETS_PREVIOUS_KEYS`.
3. Roll API and orchestrator. Both can decrypt old values while new writes use the new key.
4. Re-encrypt or rewrite secrets at rest, then remove the old key from previous keys after the retention window.
5. Rotate provider credentials/API keys separately and update the project secret through the API.
6. Verify: a secret read succeeds, a new secret write/read succeeds, and no decrypt errors appear in logs.

## 10. Database restoration

**Detect:** data loss, failed migration, corrupted relations, disaster recovery drill.

1. Stop writers (scale API/orchestrator to zero) and preserve the failed database if possible.
2. Take/verify the latest logical or PITR backup. For the Compose base, `make backup-verify` restores a scratch
   database and checks core table counts; for managed PostgreSQL, run the provider's restore verification.
3. Restore into a new database, point `*_DATABASE_URL` at it, and run the migration job to confirm the schema
   version.
4. Start API and orchestrator, then confirm `/readyz`, audit events and a smoke execution.
5. Reconcile worker slots if restoring while workers were active: restart workers after the restore.
6. Verify row counts for executions, tasks, attempts, checkpoints, outbox and audit events against the backup
   manifest. Record the RPO/RTO achieved.

## Escalation

- Sev 1: customer executions failing or data at risk. Page the platform on-call and database owner immediately.
- Sev 2: degraded throughput or one component unavailable with durable work queued. Notify the on-call channel.
- Sev 3: single-tenant or non-urgent issue. Create a ticket with the alert name, time range and request IDs.

Every incident gets a blameless review within five business days. Link the failing alert, dashboards, traces and
the exact remediation commands used.
