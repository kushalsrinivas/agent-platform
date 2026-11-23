# Integration tests

Four suites run against the Compose stack after migrations:

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
- `messaging_flow_test.go` — the Phase 05 exit criterion against real JetStream:
  - `TestMessagingRedeliveryAndDuplicateSuppression` commits a result, simulates a crash before the broker ack,
    waits for redelivery, and proves the duplicate is acknowledged as `ALREADY_FINISHED` with one attempt and one
    completion event per task.
  - `TestMessagingDeadLetterAndRedrive` poisons a dispatch until it is dead-lettered, inspects the preserved DLQ
    envelope, flips the handler, redrives and proves the task completes exactly once.

`make integration-test` builds and starts `api`, `orchestrator` and `worker` as well as the data stores, waits for
API health, then runs all suites. `ORCHESTRATOR_GRPC_ADDR` (derived from `ORCHESTRATOR_GRPC_PORT`) is passed to
the tests; `ORCHESTRATOR_ADMIN_TOKEN` is not needed because tests call the redrive function directly. The API
must be in `AUTH_MODE=dev` for the flow test; the local Compose base file does this. A VPS override defaults to OIDC and the flow test is not intended to run there.

Connection URLs default to `localhost:5432` and can be overridden with `API_URL`, `API_DATABASE_URL`,
`ORCHESTRATOR_DATABASE_URL`, `POSTGRES_DATABASE_URL`, `ORCHESTRATOR_NATS_URL` and `ORCHESTRATOR_GRPC_ADDR`. `make integration-test` builds
them from `POSTGRES_PORT` (read from `.env` when present, otherwise 5432).

The tests create uniquely named organisations and clean them up in dependency order. They need no external network
and call no model provider.
