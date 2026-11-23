# ADR 0008: Checkpoint serialization

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [data-ownership.md](../data-ownership.md), [proto/runtime/v1/runtime.proto](../../proto/runtime/v1/runtime.proto), [ADR 0006](0006-idempotency-and-side-effects.md)

## Context

Agent executions can run for minutes and make many model/tool steps. If a worker dies, restarting from the
beginning wastes tokens and may repeat effects. We need to persist agent-internal state between attempts without
letting Python write the database or define durability semantics.

The state is Python-specific (message history, scratchpad, loop counters, tool outputs) and may not be safely
deserializable with a library that executes code. It may also contain tenant data or prompt content, so it needs
the same access controls as execution data and must never contain credentials.

## Decision

### Where it lives

- Checkpoints are **immutable blobs in object storage**, referenced by a row in `execution.checkpoints`. PostgreSQL
  stores only metadata: object key, SHA-256, size, schema version, runtime ABI/version, agent version ID, owner
  attempt.
- The Python runtime serialises the bytes. The **Go worker** uploads them via a presigned URL. Python never holds
  storage credentials and never talks to S3 directly.
- The reference is committed by the orchestrator only after the upload hash/size are reported by the worker and the
  attempt passes the fence. An uploaded blob without a committed reference is an orphan and is garbage-collected.

### Format

A checkpoint is a single object, compressed with zstd, containing UTF-8 JSON:

```json
{
  "checkpoint_version": 1,
  "state_schema_version": 1,
  "runtime": {
    "abi_version": 1,
    "package": "agent-runtime",
    "version": "0.3.0",
    "image_digest": "sha256:..."
  },
  "agent_version_id": "agtv_...",
  "execution_id": "exec_...",
  "task_id": "task_...",
  "node_id": "research",
  "step": 7,
  "created_at": "2026-11-22T12:03:04.000Z",
  "state": {}
}
```

- JSON (not pickle, not arbitrary binary) so the control plane can inspect metadata, enforce limits and scan for
  secrets without executing code. The `state` object is opaque to Go.
- No credentials, API keys, headers or raw provider tokens in any field. The runtime scrubs them before
  serialisation; the worker may perform a lightweight secret scan and reject obvious leaks.
- Maximum uncompressed size **32 MiB**; larger states fail the attempt with `CHECKPOINT_TOO_LARGE` rather than
  being silently truncated.
- Encryption at rest is object-storage SSE-KMS. Access is via presigned URL scoped to the attempt.

### When

- `checkpoint_mode`: `none` or `on_node_end` (default). Phase 1 commits a checkpoint **only together with a task
  result** (success, failure or cancellation). The runtime serialises state at node end, after the last tool call
  and before returning; it must not claim that tool calls are transactional.
- A worker crash before the result is reported leaves only an orphan blob: the checkpoint reference was never
  committed, so a retry starts from the previous committed checkpoint (often none). Mid-node streaming checkpoints
  would require a fenced `CommitCheckpoint` RPC and are deferred until a real workflow needs them.
- Checkpoint failure is non-fatal by default: the result can still succeed, with a warning event. If the node sets
  `require_checkpoint`, a failed upload fails the attempt.

### Resume rules

On retry, the worker fetches the latest committed checkpoint for the task, verifies its SHA-256 and size, and passes
its bytes plus metadata to the runtime. The runtime accepts it only if:

- `checkpoint_version` is supported;
- `runtime.abi_version` matches the current runtime;
- `agent_version_id` matches the pinned version;
- `state_schema_version` is supported by this runtime version.

Otherwise the attempt fails `CHECKPOINT_INCOMPATIBLE`. The node may restart from scratch only if the workflow
explicitly allows `restart_on_incompatible` (default false) or has no checkpoint requirement. A mismatch is never
silently ignored.

Checkpoints are resume optimisations, not exactly-once boundaries. A resume may replay tool calls made after the
last checkpoint; side-effect safety is governed by ADR 0006.

## Consequences

**Positive**

- Large states do not bloat PostgreSQL or NATS; the database stays fast and backup-friendly.
- Format is inspectable, versioned and not code-executing; secrets can be scanned.
- ABI/agent version checks prevent resuming state into an incompatible runtime.
- Checkpoint loss degrades to a longer retry rather than data corruption.

**Negative**

- Python state must be serialisable and reasonably sized; live handles, sockets and locks cannot be checkpointed.
- Object storage becomes a dependency for resume (not for basic task success unless required).
- JSON+zstd is larger than pickle for some structures; acceptable for inspectability.
- Retention/GC must coordinate DB rows and object lifecycle.

## Alternatives considered

- **Python `pickle`.** Arbitrary code execution on load, version-fragile across library upgrades, opaque to
  inspection. Rejected.
- **Checkpoint bytes in PostgreSQL (`bytea`/TOAST).** Bloats WAL/backups and competes with state writes; capped
  checkpoints can still be tens of MiB.
- **Event sourcing the agent loop.** Ideal replay but requires a total order of model/tool events and deterministic
  re-execution; not viable with non-deterministic LLM calls in Phase 1.
- **No checkpoints.** Simplest, but long agents lose all progress on a worker crash and repeat expensive calls.
- **Runtime writes to S3 directly.** Spreads storage credentials into untrusted code. Rejected.

## Verification

- Round-trip tests across supported ABI/schema versions; rejection tests for each mismatch.
- Oversized checkpoint test fails explicitly; no truncation.
- Secret-scan test plants a credential and asserts rejection/alert.
- Fault-injection: kill the worker between upload and result; blob is orphaned and GC'd, no DB reference exists.
- Resume test proves retry starts at the latest committed step and preserves side-effect policy.
