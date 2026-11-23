# Integration tests

Five suites run against the Compose stack after migrations:

```bash
make integration-test
```

- `infrastructure_test.go` — schemas, role separation and JetStream availability.
- `schema_test.go` — RLS (with, without and cross tenant), org discovery, write/read role separation, idempotency
  uniqueness, lease/attempt constraints, terminal-state checks, version immutability, append-only events, closed
  attempt immutability, and outbox claim/mark/skip-locked behaviour.
- `api_flow_test.go` — the Phase 04 exit criterion over real HTTP: dev token → organisation → project → agent
  version → publish → workflow version → publish → execution; then verifies the committed execution, tasks,
  dependencies, audit event and outbox row in PostgreSQL, idempotent replay/conflict, RBAC and cross-tenant 404.
  Since Phase 05 schedules immediately, the task/outbox assertions accept a live or completed execution.
- `messaging_flow_test.go` — the Phase 05 exit criterion against real JetStream and the Phase 06 worker-manager
  scenarios:
  - `TestMessagingRedeliveryAndDuplicateSuppression` commits a result, simulates a crash before the broker ack,
    waits for redelivery, and proves the duplicate is acknowledged as `ALREADY_FINISHED` with one attempt and one
    completion event per task.
  - `TestMessagingDeadLetterAndRedrive` poisons a dispatch until it is dead-lettered, inspects the preserved DLQ
    envelope, flips the handler, redrives and proves the task completes exactly once.
  - `TestWorkerManagerDistributesTasks` starts two capacity-1 workers and proves they receive different tasks.
  - `TestWorkerManagerPriorityOrder` proves a priority-100 execution is assigned before a normal one.
  - `TestWorkerManagerTenantFairness` proves two tenants each get work in the same dispatch pass.
  - `TestWorkerManagerLeaseRenewal` proves heartbeats extend an executing task's lease.
  - `TestWorkerManagerStaleWorkerCannotCommit` reclaims a dead worker's lease, completes the retry on another
    worker, and proves the old result returns `STALE_LEASE` without changing state.
  - `TestWorkerManagerDrainStopsAssignments` proves a draining worker receives no new tasks.
  - `TestWorkerManagerBootChangeReclaimsAttempts` proves a new boot for the same worker ID reclaims the previous
    process's attempt immediately.
  - `TestRuntimeLangGraphAgentExecution` executes a real LangGraph agent through the Python runtime and asserts the
    schema-validated report, runtime version, token usage and attempt event.
  - `TestRuntimeOutputSchemaRejectsInvalidOutput` proves invalid runtime output is reported as
    `RESULT_SCHEMA_INVALID` and never committed as a success.
- `recovery_flow_test.go` — the Phase 08 durable-execution suite:
  - `TestRecoveryWorkerDiesBeforeReceiving` and `TestRecoveryWorkerDiesAfterReceiving` reclaim an assigned or
    running attempt, retry it on a live worker and finish the execution.
  - `TestRecoveryWorkerDiesDuringModelGeneration` kills the runtime before any checkpoint; the next attempt starts
    fresh and succeeds.
  - `TestRecoveryResumesCompletedCheckpoint` kills the worker after the completed checkpoint, then proves the retry
    returns the stored output without another model call and writes no duplicate checkpoint.
  - `TestRecoveryDoesNotRepeatCheckpointedToolEffect` crashes after the tool boundary, resumes after the tool and
    proves the side effect ran exactly once.
  - `TestRecoveryOrchestratorRestart` queues work, cancels another execution, restarts the orchestrator and proves
    the queued work still runs and the cancelled execution stays cancelled with no attempts.
  - `TestRecoveryNATSReconnect` restarts NATS during an attempt and proves the execution completes.
  - `TestRecoveryDatabaseUnavailable` pauses PostgreSQL briefly and proves committed work still completes.
  - `TestRecoveryExecutionDeadline` proves a deadline with no worker finalises `failed` with
    `EXECUTION_TIMEOUT`, not a permanent pending state.
  - The remaining mandatory scenarios are covered by `TestMessagingRedeliveryAndDuplicateSuppression`,
    `TestWorkerManagerStaleWorkerCannotCommit` and the checkpoint tests above.

The recovery suite restarts/pauses Compose services through the Docker CLI; it skips only when the Docker daemon is
unavailable. The runtime tests use the host venv at `runtimes/python/.venv`; `make integration-test` runs `uv sync --frozen`
first. Set `RUNTIME_PYTHON` to override the interpreter path.

`make integration-test` builds and starts `api`, `orchestrator` and `worker` as well as the data stores, waits for
API health, then runs all suites. `ORCHESTRATOR_GRPC_ADDR` (derived from `ORCHESTRATOR_GRPC_PORT`) is passed to
the tests; `ORCHESTRATOR_ADMIN_TOKEN` is not needed because tests call the redrive function directly. The API
must be in `AUTH_MODE=dev` for the flow test; the local Compose base file does this. A VPS override defaults to OIDC and the flow test is not intended to run there.

Connection URLs default to `localhost:5432` and can be overridden with `API_URL`, `API_DATABASE_URL`,
`ORCHESTRATOR_DATABASE_URL`, `POSTGRES_DATABASE_URL`, `ORCHESTRATOR_NATS_URL` and `ORCHESTRATOR_GRPC_ADDR`. `make integration-test` builds
them from `POSTGRES_PORT` (read from `.env` when present, otherwise 5432).

The tests create uniquely named organisations and clean them up in dependency order. They need no external network
and call no model provider.
