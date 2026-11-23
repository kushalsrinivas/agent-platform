# ADR 0007: Workflow graph representation

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [state-machines/task.md](../state-machines/task.md), [api/openapi.yaml](../api/openapi.yaml)

## Context

We need a workflow representation that is easy for a UI to edit, safe to execute, immutable once published, and
unambiguous about parallel branches, joins and conditional paths. Two bad options are common: a general-purpose
script (hard to validate, unsafe in the control plane) and an under-specified graph (ambiguous join semantics and
runtime races).

We also need deterministic materialisation: at submission time, the set of tasks and dependencies must be created
without consulting mutable state later.

## Decision

### Structure

- A workflow version contains a **DAG** of `nodes` and `edges`. Nodes are `agent` or `tool`. No cycles, validated
  at publish.
- Each agent/tool node references an **exact immutable version** (`agent_id + version`, `tool_id + version`). The
  string `latest` is not accepted in a published graph.
- Edges may carry an optional CEL boolean expression `when`, evaluated over upstream outputs and execution input.
  No other scripting is allowed in the control plane.
- A node may declare `input_from: [node_id...]` to select which upstream outputs are passed to it. The runtime
  always receives `execution.input`, `node.params`, and the selected upstream outputs keyed by node ID. Execution
  input, node params and node outputs are JSON **objects** (`google.protobuf.Struct` on the wire); scalars/arrays
  must be wrapped in an object. Every successful node returns an inline output object (≤64 KiB) even when it also
  stores a larger artifact, because downstream `when` edges evaluate over the inline outputs. This keeps the
  edge/CEL environment and storage schema uniform.
- Workflow-level `policy.on_failure` is `fail_fast` (default) or `continue`.
- Node-level fields: timeout, retry policy, side-effect class, idempotency key, checkpoint mode, output schema.

### Activation semantics

- Root nodes always run.
- A node activates if **at least one** inbound edge evaluates `true`.
- If no edge is `true`:
  - any edge is `unavailable` because its upstream failed/skipped → task `skipped` with
    `dependency_failed`;
  - otherwise → task `skipped` with `branch_not_taken`.
- Cascading from an upstream skip is `upstream_not_taken`, not a failure.
- A join is expressed by adding **unconditional edges** from each branch. Because activation is OR, the join runs
  when any branch arrives; to require all branches, the author expresses that condition inside the join node or
  uses explicit edges. This is a deliberate simplification: AND-joins with "wait for all" semantics are ambiguous
  when combined with conditional branches and deadlocks are easy to create.
- CEL evaluation errors fail the dependent task with `EDGE_EVALUATION_FAILED` (terminal, no attempt). Silent
  fallback is forbidden because it could skip work invisibly.

### Materialisation

- At execution creation, the orchestrator copies every node into an `execution_tasks` row and every edge into
  `task_dependencies`, resolved against the pinned graph. No nodes are added later.
- Dynamic fan-out and loops are **not supported** in Phase 1. A node may loop internally (agent turns), but it
  cannot create new tasks. Adding dynamic task creation requires an ADR and a rethinking of materialisation,
  cancellation and completion.
- Conditional edges are evaluated by the scheduler when all upstream dependencies are terminal; the skip decision
  is a transaction, not a runtime side effect.
- `input_from` cannot reference a node that is not an upstream dependency; this is validated.

### Versioning

- A workflow version is `draft` or `published`. Versions are insert-only: the graph is immutable from creation, and
  publishing is a one-way status transition. Editing a graph creates a new version.
- An execution references one `workflow_version_id` for its whole life.
- Edits create a new version. Rollback means starting an execution from an older published version, not mutating
  the current one.

## Consequences

**Positive**

- Validation can be complete and static: DAG, exact versions, CEL compilation, input references.
- Execution shape is known at submission; scheduling and completion are straightforward.
- The UI can render the graph and branches without executing code.
- OR-activation plus unconditional joins removes deadlock ambiguity.

**Negative**

- No dynamic fan-out or recursive workflows; some agent patterns need to be encoded inside a node.
- AND-join semantics must be explained to authors; the graph cannot express "wait for all live branches" natively.
- CEL is expressive enough for routing but not for data transformation; authors may need a tool node for complex
  mapping.
- `input_from` is a limited data-flow language; users may expect arbitrary expressions.

## Alternatives considered

- **Arbitrary scripts (Python/JS) as workflow definitions.** Maximum power, but unsafe and impossible to validate
  statically; would undermine the control-plane/runtime split.
- **Temporal-style code workflows.** Excellent for dynamic control flow, but a large runtime dependency and a
  different authoring model; not needed for the initial product.
- **BPMN.** Rich, but heavy, XML-oriented and still needs custom execution semantics.
- **AND-joins with per-edge conditions.** Common source of deadlocks and "why didn't this run" bugs; rejected in
  favour of explicit OR activation plus unconditional joins.
- **JSONata/JavaScript for edge conditions.** CEL is non-Turing-complete, sandboxable in Go and cheap to validate.

## Verification

- Graph validation tests: cycles, duplicate node IDs, missing/extra agent/tool refs, unpublished versions, invalid
  CEL, `input_from` not an upstream, oversized graph.
- Activation truth-table tests: true/false/unavailable/error, OR semantics, cascading skip reasons.
- Materialisation tests prove all tasks exist before any worker is assigned.
- Version-immutability tests prove a published graph cannot be edited or used with a different agent version.
