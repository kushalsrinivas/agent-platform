# ADR 0001: Go control plane, Python agent/tool runtime

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [architecture.md](../architecture.md), [communication.md](../communication.md), [proto/runtime/v1/runtime.proto](../../proto/runtime/v1/runtime.proto)

## Context

Agent execution needs the Python ecosystem (LLM SDKs, tool/MCP clients, serialization, data libraries). Durable
execution needs strict transaction boundaries, efficient concurrency, predictable binaries and long-lived
connections. Mixing both concerns in one runtime has historically produced services where request handlers and GPU
work share a process, state is mutated from callbacks, and a Python exception can strand an execution.

We also need a hard boundary for trust: agent code and tool code are the least trusted part of the system. The
component allowed to declare success must not be the component that ran the model.

## Decision

- The **control plane is Go**: `api` (gateway + control-plane CRUD), `orchestrator` (execution/task state,
  scheduler, worker manager, publisher) and `worker` (lease holder and sandbox host).
- The **data plane is Python**: `agent-runtime` and `tool-runtime`, invoked by the worker as a short-lived process
  per task attempt over a per-attempt Unix-domain gRPC socket.
- Go owns every state transition, every transaction, every lease and every decision. Python owns computation only.
  It receives an immutable task context and returns a fenced result plus an optional checkpoint.
- The boundary is a versioned protobuf contract. No shared database, no shared memory, no Python writing NATS.
- The orchestrator validates Python's result before committing it. Python's "succeeded" is a request, not a fact.

## Consequences

**Positive**

- State correctness is testable in one language with explicit transactions and row locks.
- Workers crash without taking down the control plane; Python crashes only the attempt.
- Go binaries are easy to deploy and profile; Python dependencies remain isolated in the runtime image.
- The trust boundary matches the data boundary: Python never holds database or broker credentials.

**Negative**

- Two toolchains, two CI pipelines, two dependency ecosystems.
- Task context, outputs and checkpoints cross a serialization boundary and need schema discipline.
- Worker packaging must carry a Python runtime image; local development needs both toolchains.
- The protobuf contract must be versioned and compatibility-tested.

**Neutral / operational**

- The worker process supervises child processes and must reap them reliably.
- Debugging a task spans Go logs and Python spans; trace context must be propagated end to end.

## Alternatives considered

- **All Python control plane.** Simpler one-language stack, but weak fit for high-concurrency lease/fencing logic,
  GIL-sensitive scheduling, slower cold starts, and it places state authority in the untrusted runtime. Rejected.
- **All Go, embed Python via FFI.** Avoids a process boundary but a Python crash or memory leak can kill the
  worker; dependency conflicts and GIL make it operationally fragile. Rejected.
- **Temporal or another workflow engine as the control plane.** Solves durability but imports a large operational
  dependency and a different programming model; we would still need to model tenant-scoped workers and Python
  sandboxes. Deferred, not rejected forever; would need an ADR.
- **Python control plane with a Go sidecar for state.** Two writers and ambiguous ownership. Rejected.

## Verification

- Python has no database/broker client in its dependency set; CI asserts this.
- State-machine tests run only against the Go store; Python runtime tests cannot transition tasks.
- A result emitted by the runtime without a valid `(attempt_id, lease_id)` is rejected in integration tests.
- The runtime proto and worker proto compile in CI and are checked for breaking changes.
