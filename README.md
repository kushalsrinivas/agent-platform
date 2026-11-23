# Agent Platform

A multi-tenant platform for running versioned agent workflows with durable execution state.

**Phase 01 is complete when the documentation in [`docs/`](docs/README.md) lets an engineer explain how an
execution starts, how work reaches a worker, how results are committed, where state lives, what happens when a
worker dies, and how duplicate messages are neutralised — without reading code.**

No application code is written in Phase 01. The architecture, contracts, state machines and decisions are the
deliverable. Implementation starts in Phase 02 against these documents.

## Repository layout (Phase 01)

```
docs/
  architecture.md              System architecture and invariants
  service-responsibilities.md  Which deployable does what, owns what
  communication.md             Protocols, message envelope, subjects, versioning
  data-ownership.md            Tables, schemas, roles, object storage, retention
  state-machines/
    execution.md               Execution lifecycle and completion rules
    task.md                    Task lifecycle, attempts, leases, retries
  failure-and-recovery.md      Failure matrix and recovery behaviour
  api/
    openapi.yaml               Initial external REST/SSE contract
    envelope.schema.json       Internal message envelope (JSON Schema)
  adr/                         15 mandatory architecture decision records
proto/
  worker/v1/worker.proto       Worker <-> orchestrator gRPC contract
  orchestrator/v1/commands.proto  API <-> orchestrator command contract
  runtime/v1/runtime.proto     Worker <-> Python runtime contract
```

## Reading order

1. [`docs/architecture.md`](docs/architecture.md) — the ten-minute version.
2. [`docs/state-machines/`](docs/state-machines/) — the two state machines everything else depends on.
3. [`docs/data-ownership.md`](docs/data-ownership.md) and [`docs/communication.md`](docs/communication.md).
4. [`docs/adr/`](docs/adr/README.md) — why the above looks the way it does.
5. [`docs/failure-and-recovery.md`](docs/failure-and-recovery.md) — the unhappy paths.

## The one-paragraph version

Go control plane (`api`, `orchestrator`) and a Go worker host own all decisions and state. PostgreSQL is the
single source of truth. NATS JetStream only transports work; losing or duplicating a message never changes the
logical outcome. Python (`agent-runtime`, `tool-runtime`) executes one task attempt inside a sandbox and reports
a result; it never writes the database, never publishes events and never decides that a workflow finished. The
orchestrator fences every result with a per-attempt lease, validates it, and commits the state transition. Every
task attempt is individually identifiable. Every execution pins immutable workflow and agent versions.
