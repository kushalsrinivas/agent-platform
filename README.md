# Agent Platform

A multi-tenant platform for running versioned agent workflows with durable execution state.

Go control plane, PostgreSQL source of truth, NATS JetStream transport, Python execution sandbox, Next.js
dashboard.

## Quick start

Only Docker with Compose v2 is required to run the stack:

```bash
make dev     # build images, run migrations, start everything
```

Then open http://localhost:3000. API health is at http://localhost:8080/healthz. Build a workflow visually at
http://localhost:3000/workflows/new.

Host ports can be remapped in `.env` if another local project owns them; all services and test commands derive
their URLs from those values.

For local tests, linters or running services outside Docker, install Go 1.26+, Node.js 20.9+ and
[uv](https://docs.astral.sh/uv/), then:

```bash
make setup   # verify tools, install dependencies, create .env
```

Every command, port and troubleshooting step is in [`docs/development.md`](docs/development.md). Single-host / VPS
deployment is in [`docs/deployment.md`](docs/deployment.md).

### Developer quick start

Create a project and an API key (owner token), then use the SDK or CLI:

```bash
pip install ./sdk/python
export AGENT_PLATFORM_URL=http://localhost:8080
export AGENT_PLATFORM_API_KEY=apk_...
export AGENT_PLATFORM_ORG=org_...
export AGENT_PLATFORM_PROJECT=proj_...
python examples/python/01_simple_llm_agent.py
```

```bash
make build-go
bin/agentctl login --api-url "$AGENT_PLATFORM_URL" --api-key "$AGENT_PLATFORM_API_KEY"
bin/agentctl init --dir demo --name hello
bin/agentctl project create --name "Demo" --slug demo
bin/agentctl agent create --file demo/agent.json
# replace agt_REPLACE_WITH_AGENT_ID in demo/workflow.json, then:
bin/agentctl workflow create --file demo/workflow.json --deploy
bin/agentctl dev --file demo/workflow.json --input demo/input.json --no-worker
```

The developer guide starts at [`docs/developer/index.md`](docs/developer/index.md); the documentation site
configuration is [`mkdocs.yml`](mkdocs.yml).

## Repository layout

```
apps/dashboard/          Next.js dashboard: live API, visual workflow builder, execution explorer and SSE
apps/docs/               Hosted docs site (deferred; docs live in /docs)
services/api/            Go HTTP control plane: auth, REST/SSE, command forwarding
services/orchestrator/   Go execution owner: scheduler, leases, retries, events
services/worker/         Go attempt host and Python sandbox supervisor
runtimes/python/         agent_runtime, model_providers, tool_runtime, mcp, checkpointing
sdk/go/                  Go developer SDK
sdk/python/              Python developer SDK (sync + async) and compatibility client
cli/                     agentctl developer CLI (and operator platformctl)
proto/                   Wire contracts; gen/ holds generated Go (committed)
packages/                Shared contracts, schemas and fixtures
migrations/              control/ then execution/, applied in order
infrastructure/          Dockerfiles, compose, Kubernetes/Helm/monitoring placeholders
tests/                   Integration, e2e, load and chaos suites
docs/                    Architecture, state machines, ADRs, API contracts, development
```

Service builds are independent (`go build ./services/api/...`) and share one root Go module. Dependency injection
is manual constructor wiring in each `main.go`. Details:
[development.md § Go module layout](docs/development.md#go-module-layout).

## The architecture in one paragraph

Go control plane (`api`, `orchestrator`) and a Go worker host own all decisions and state. PostgreSQL is the
single source of truth. NATS JetStream only transports work; losing or duplicating a message never changes the
logical outcome. Python (`agent-runtime`, `tool-runtime`) executes one task attempt inside a sandbox and reports
a result; it never writes the database, never publishes events and never decides that a workflow finished. The
orchestrator fences every result with a per-attempt lease, validates it, and commits the state transition. Every
task attempt is individually identifiable. Every execution pins immutable workflow and agent versions.

## Status

| Phase | State |
|---|---|
| 01 — Architecture and contracts | Complete. Normative docs in [`docs/`](docs/README.md). |
| 02 — Repository and development environment | Complete. Monorepo, local stack, CI and dashboard shell. |
| 03 — Database architecture and shared contracts | Complete. 17 tables, RLS, immutable versions, workflow/task/event schemas, Go/Python models, outbox. |
| 04 — Go API and control plane | Complete. OIDC/dev auth, org/project/member/agent/workflow APIs, durable execution submission, quotas. |
| 05 — Scheduler, worker and event pipeline | Complete. JetStream dispatch/DLQ, outbox publisher, scheduler, fenced results, redelivery and redrive. |
| 06 — Worker manager and distributed scheduler | Complete. Worker capabilities/resources, heartbeat statuses, priority and tenant-fair scheduling, leases/fencing, graceful drain. |
| 07 — Python agent runtime | Complete. LangGraph agent loop over the runtime ABI, provider adapters, schema-validated results, usage, cancellation and runtime tests. |
| 08 — Durable execution, checkpoints and recovery | Complete. Durable checkpoints, fenced resume, recovery manager, retry engine, idempotency keys, durable cancellation and a ten-scenario crash suite. |
| 09 — DAG workflow execution engine | Complete. Explicit node types, CEL routing, parallel/join, data mappings, durable delay/approval, child subworkflows/loops, replay and a 12-node recovery test. |
| 10 — Multi-agent orchestration, delegation and MCP | Complete. Supervisor delegation, budgets, parent-child tracking, durable agent events, scoped context, MCP transports and tool policies, human approvals. |
| 11 — Developer API, SDKs and CLI | Complete. Canonical `/api/v1`, API keys, environments, developer resources, Python/Go SDKs, `agentctl`, local dev mode, ten examples and a docs site. |
| 12 — Developer dashboard and execution explorer | Complete. Live overview, agents, workflows, execution graph, SSE timeline, node/model/failure inspectors, logs, approvals, workers, tools, MCP, usage and settings. |
| 13 — Visual workflow builder | Complete. Canvas editing, categorized node library, inspectors, local/server validation, immutable versions, deploy/rollback, comparison and live execution in the builder. |

Phase 13 is documented in [`docs/phase-13-visual-builder.md`](docs/phase-13-visual-builder.md); Phase 12 is documented in [`docs/phase-12-dashboard.md`](docs/phase-12-dashboard.md); Phase 11 is documented in
[`docs/phase-11-developer-api.md`](docs/phase-11-developer-api.md); Phase 10 is documented in
[`docs/phase-10-multi-agent-mcp.md`](docs/phase-10-multi-agent-mcp.md); Phase 09 is documented in
[`docs/phase-09-graph-engine.md`](docs/phase-09-graph-engine.md); Phase 08 is documented in
[`docs/phase-08-durable-execution.md`](docs/phase-08-durable-execution.md); Phase 07 is documented in
[`docs/phase-07-agent-runtime.md`](docs/phase-07-agent-runtime.md); Phase 06 is documented in
[`docs/phase-06-worker-manager.md`](docs/phase-06-worker-manager.md); Phase 05 is documented in
[`docs/phase-05-messaging.md`](docs/phase-05-messaging.md); Phase 04 is documented in
[`docs/phase-04-control-plane.md`](docs/phase-04-control-plane.md); Phase 03 details are in
[`docs/phase-03-contracts.md`](docs/phase-03-contracts.md).

## Reading order

1. [`docs/architecture.md`](docs/architecture.md) — the ten-minute version.
2. [`docs/state-machines/`](docs/state-machines/) — the two state machines everything else depends on.
3. [`docs/data-ownership.md`](docs/data-ownership.md) and [`docs/communication.md`](docs/communication.md).
4. [`docs/adr/`](docs/adr/README.md) — why the above looks the way it does.
5. [`docs/failure-and-recovery.md`](docs/failure-and-recovery.md) — the unhappy paths.
6. [`docs/development.md`](docs/development.md) — how to run and change it.
7. [`docs/deployment.md`](docs/deployment.md) — running it on a single host.
