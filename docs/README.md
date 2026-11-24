# Documentation Index — Phase 01 architecture, Phase 02 development

This directory is the normative architecture for the agent platform. Where a document and an ADR disagree, the
ADR wins and the document is a bug. Code written in later phases must cite and follow these documents.

| Document | Answers |
|---|---|
| [development.md](development.md) | How to set up, run, test, migrate and reset the local stack. |
| [deployment.md](deployment.md) | Single-host/VPS deployment, credentials, backups and upgrades. |
| [phase-03-contracts.md](phase-03-contracts.md) | Phase 03 schema, workflow/task/event contracts, outbox and brief mapping. |
| [phase-04-control-plane.md](phase-04-control-plane.md) | Phase 04 auth, API surface, execution submission and quotas. |
| [phase-05-messaging.md](phase-05-messaging.md) | Phase 05 streams, outbox publisher, scheduler, durable delivery, duplicates, DLQ and redrive. |
| [phase-06-worker-manager.md](phase-06-worker-manager.md) | Phase 06 worker registry, heartbeat statuses, priority/fair scheduling, leases, fencing and graceful drain. |
| [phase-07-agent-runtime.md](phase-07-agent-runtime.md) | Phase 07 Python runtime ABI, LangGraph adapter, providers, structured results, cancellation and observability. |
| [phase-08-durable-execution.md](phase-08-durable-execution.md) | Phase 08 checkpoint schema and boundaries, resume protocol, recovery manager, retry engine, idempotency, durable cancellation and crash tests. |
| [phase-09-graph-engine.md](phase-09-graph-engine.md) | Phase 09 DAG node types, validator, executor registry, CEL routing, parallel/join, data mappings, delay/approval, subworkflows/loops and replay. |
| [phase-10-multi-agent-mcp.md](phase-10-multi-agent-mcp.md) | Phase 10 supervisor delegation, parent-child tracking, delegation policies, durable agent events, MCP transports, tool policies and human approvals. |
| [phase-11-developer-api.md](phase-11-developer-api.md) | Phase 11 canonical `/api/v1`, API keys, environments, developer resources, Python/Go SDKs, `agentctl`, local dev mode, examples and developer docs. |
| [architecture.md](architecture.md) | What the system is, its boundaries, its invariants, and how the pieces cooperate. |
| [service-responsibilities.md](service-responsibilities.md) | What each deployable does, reads, writes, and when it may become a separate service. |
| [communication.md](communication.md) | External and internal protocols, envelope, subjects, delivery guarantees, versioning. |
| [data-ownership.md](data-ownership.md) | Who owns each table and schema, what may be read, where blobs live, how long data lives. |
| [state-machines/execution.md](state-machines/execution.md) | Execution states, transitions, guards, completion and cancellation rules. |
| [state-machines/task.md](state-machines/task.md) | Task states, attempts, leases, retries, skip and failure rules. |
| [failure-and-recovery.md](failure-and-recovery.md) | What happens when each component dies at each point in the protocol. |
| [developer/index.md](developer/index.md) | Developer quick start, agents, workflows, execution lifecycle, SDKs, CLI, MCP, debugging, recovery and examples. |
| [api/openapi.yaml](api/openapi.yaml) | Public REST/SSE contract. `/api/v1` canonical, API keys, errors, idempotency, SSE. |
| [api/envelope.schema.json](api/envelope.schema.json) | Internal message envelope, version 1. |
| [adr/README.md](adr/README.md) | Index of the 15 mandatory decisions. |
| [../proto/](../proto/) | Initial gRPC contracts. |

## Deliverables

| Phase 01 deliverable | Where |
|---|---|
| Architecture document | [architecture.md](architecture.md) |
| Service responsibility matrix | [service-responsibilities.md](service-responsibilities.md) |
| System communication diagram | [architecture.md §3](architecture.md#3-system-context) and [communication.md §2](communication.md#2-channel-summary) |
| Database ownership document | [data-ownership.md](data-ownership.md) |
| ADR directory (15 mandatory decisions) | [adr/](adr/README.md) |
| Initial API contract | [api/openapi.yaml](api/openapi.yaml), [api/envelope.schema.json](api/envelope.schema.json) |
| Execution state machine | [state-machines/execution.md](state-machines/execution.md) |
| Task state machine | [state-machines/task.md](state-machines/task.md) |
| Failure and recovery design | [failure-and-recovery.md](failure-and-recovery.md) |

## Exit-criteria map

An engineer can explain all six without reading code if they have read the sections below.

| Question | Primary section |
|---|---|
| How a workflow starts | [architecture.md §9.1](architecture.md#91-how-a-workflow-starts) |
| How a task reaches a worker | [architecture.md §9.2](architecture.md#92-how-a-task-reaches-a-worker) |
| How the worker returns its result | [architecture.md §9.3](architecture.md#93-how-a-worker-returns-its-result) |
| Where execution state is stored | [architecture.md §9.4](architecture.md#94-where-execution-state-is-stored) |
| What happens when a worker crashes | [architecture.md §9.5](architecture.md#95-what-happens-when-a-worker-crashes) |
| How duplicate messages are handled | [architecture.md §9.6](architecture.md#96-how-duplicate-messages-are-handled) |

## Change rule

Changing a state, an ownership boundary, a message schema or a guarantee requires an ADR first, then the affected
documents in the same change. State-machine tables and OpenAPI are normative, not illustrative.
