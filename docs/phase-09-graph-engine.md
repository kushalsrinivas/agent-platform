# Phase 09 — DAG workflow execution engine

Status: complete. The platform now executes a graph, not just a bag of tasks. The Go orchestrator owns
platform-level graph semantics; LangGraph remains the agent-internal reasoning loop and never decides workflow
routing.

The exit criterion is met and tested against the live stack: a 12-node workflow with explicit start/end, parallel
fan-out, join-all fan-in, CEL conditional routing, retries and a worker crash executes and recovers successfully
(`TestGraphEngineTenNodeWorkflow`).

## 1. Graph representation

A workflow is `G = (V, E)`. Nodes have a unique id, a type, type-specific configuration, optional input/output
schemas, retry policy, timeout, resource requirements, side-effect class and checkpoint settings. Edges have a
source, target, optional CEL condition, optional data mapping and priority.

| Node type | Execution |
|---|---|
| `start`, `end` | Go control nodes |
| `agent`, `llm` | Python runtime through the worker |
| `tool` | Python tool runtime through the worker |
| `condition` | Go CEL boolean |
| `transform` | Go CEL mapping (restricted data transformation) |
| `parallel` | Go fan-out marker; branches are ordinary graph tasks |
| `join` | Go fan-in with `all` or `successful N` policy |
| `delay` | Durable scheduler state (`seconds`, `until`, or external `event`) |
| `approval` | Human decision delivered through the API |
| `subworkflow` | Child execution manager |
| `loop` | Bounded child executions with an exit condition |

Example:

```json
{
  "nodes": [
    {"id": "start", "type": "start"},
    {"id": "research", "type": "agent", "agent": {"agent_id": "agt_...", "version": 1}},
    {"id": "fan", "type": "parallel"},
    {"id": "web", "type": "tool", "tool": {"tool_id": "tool_web_search", "version": 1}},
    {"id": "join", "type": "join", "config": {"join": {"mode": "all"}}},
    {
      "id": "quality", "type": "condition",
      "config": {"expression": "input.score >= 80"}
    },
    {
      "id": "approve", "type": "transform",
      "config": {"mapping": {"decision": "'approved'", "report": "outputs.research.report"}}
    },
    {"id": "end", "type": "end"}
  ],
  "edges": [
    {"from": "start", "to": "research"},
    {"from": "research", "to": "fan"},
    {"from": "fan", "to": "web", "priority": 1},
    {"from": "web", "to": "join"},
    {"from": "join", "to": "quality"},
    {"from": "quality", "to": "approve", "when": "outputs.quality.result == true"},
    {"from": "approve", "to": "end", "data_mapping": {"report": "outputs.research.report"}}
  ]
}
```

### Validation

Before deployment the graph validator checks:

- unique node ids and every edge endpoint;
- a valid entry point (explicit `start` with no incoming edge, or derived roots);
- valid termination (explicit `end` with no outgoing edge and reachability from every node, or implicit terminals);
- required type-specific configuration;
- CEL compilation for every `when`, condition, transform, data mapping, subworkflow input and loop exit;
- input/output schemas compile;
- join thresholds are reachable;
- replay/subworkflow references are pinned;
- referenced agent/tool versions exist and are published at publish time;
- resource limits and retry combinations;
- cycles are rejected. Loops are explicit bounded `loop` nodes with a child workflow, not back edges.

## 2. Node executor registry

`services/orchestrator/internal/graph` defines the requested interface:

```go
type NodeExecutor interface {
    Validate(config NodeConfig) error
    Execute(ctx context.Context, input NodeInput) (NodeOutput, error)
}
```

`NodeConfig` is the pinned `contracts.WorkflowNode`. The registry contains:

- start/end/parallel pass-through executors;
- condition and transform CEL executors;
- join executor;
- delay and approval state machines;
- subworkflow and loop adapters that return a child request;
- agent, LLM and tool adapters that return `ErrWorkerRequired`, so the existing dispatcher/worker path remains the
  only way tenant code runs.

The dispatcher resolves only non-worker nodes inside the orchestrator transaction. Worker nodes still require a
live worker, lease and fenced result.

## 3. Data propagation

At dispatch time the orchestrator builds a resolved node input:

```json
{
  "input": { "...execution input..." },
  "outputs": { "research": { "report": "..." } },
  "params": { "...node params..." },
  "iteration": 0,
  "previous": { "...loop previous output..." }
}
```

- `input_from` limits which direct upstream outputs reach the worker; omitting it uses active direct inbound
  edges, not every ancestor. CEL expressions can still reference the full `outputs` map.
- Edge `data_mapping` is a map of dotted target path to CEL expression. Mappings from active edges are applied to
  the node input before the worker starts.
- The resolved input is stored as `node_input` in the task context and is passed to the Python agent prompt and tool
  arguments.
- `input_schema` on the node is validated before assignment/control execution. A mismatch fails the task as
  `INPUT_SCHEMA_INVALID`; a mapping expression failure fails as `INPUT_MAPPING_INVALID`.
- Agent/tool inline outputs remain bounded at 64 KiB. Large artifacts are represented by reference metadata and
  are not copied into every downstream message; large checkpoint state is already offloaded by Phase 08.

CEL variables are `input` (execution input), `outputs` (all succeeded node outputs), `node_input` (the resolved
input), `params`, `execution`, `iteration`, `previous` and `node`. There is no Go/Python eval.

## 4. Parallel and join semantics

Fan-out is ordinary graph edges from a `parallel` node. Each branch is an independent task with its own attempt,
state, timeout and failure tracking. The dispatcher assigns branches concurrently as workers become available.

`join` supports:

- `mode: "all"` — wait until every inbound edge is terminal; active edges must have succeeded. Conditionally
  inactive/skipped edges do not block. A failed branch fails the join unless `on_partial` says otherwise.
- `mode: "successful", successful: N` — activate as soon as N active branches succeed. If the remaining branches
  cannot reach N, the join fails or skips according to `on_partial`.
- `on_partial: fail | skip | continue` — fail the join, skip it, or continue with the successes when the threshold
  becomes impossible.

Explicit `end` nodes use join-all semantics. Ordinary nodes use OR activation: one active successful inbound edge
is enough. This is why fan-in must use a `join` (or `end`) node.

Cancellation propagates to pending tasks, active attempts and child executions. Branch timeouts are bounded by the
node timeout and the execution deadline.

## 5. Conditions and joins

Edge conditions are CEL, compiled at publication and evaluated against the current execution context:

```json
{"from": "quality", "to": "approve", "when": "outputs.quality.result == true"}
```

A condition node stores its boolean as `{"result": bool}`. Edges from it can route on that value. A skipped or
conditionally inactive branch does not fail a join; a failed branch does.

## 6. Durable delay and approval

**Delay**

- `seconds`: relative ready time;
- `until`: RFC3339 absolute time;
- `event`: wait for `POST .../tasks/{task_id}:signal`.

The ready time, waiting-for name and signal payload are persisted in `execution_tasks.control_state` and
`next_attempt_at`. No worker sleeps; the scheduler polls when due.

**Approval**

- The approval task stays pending with `control_state.status = pending`.
- Operators call `POST .../tasks/{task_id}:approve` or `:reject` with a comment.
- The decision is committed with an audit event; the dispatcher resolves the node on its next pass.
- Rejection follows `on_reject: fail | skip | continue` (`fail` by default).

## 7. Subworkflows and bounded loops

`subworkflow` and `loop` nodes use child executions. This keeps the Go graph engine independent of LangGraph and
gives every iteration its own task attempts and checkpoints.

- A child execution is materialised in the same transaction as the parent pause. It carries
  `parent_execution_id`, `parent_task_id` and `iteration`.
- The parent task stays pending and polls the child. An orchestrator restart resumes polling; nothing is kept in
  memory.
- A successful child contributes its selected succeeded outputs to the parent output. `config.output_from` selects
  node outputs; otherwise all succeeded outputs are keyed by node id.
- `loop.max_iterations` bounds the number of children (1–100). `loop.exit_when` is a CEL boolean over
  `iteration`, `previous`, `outputs`, `input` and `execution`.
- Child failures fail the parent task; parent cancellation propagates to children and their pending tasks.
- A subworkflow reference is pinned to an exact published workflow version. Self-reference is rejected at
  publication; child nesting is capped at 32 executions and loops at 100 iterations, so a workflow cannot create
  unbounded descendants.

Example loop:

```json
{
  "id": "improve",
  "type": "loop",
  "config": {
    "workflow": {"workflow_id": "wf_body", "version": 1},
    "input": {"topic": "input.topic"},
    "loop": {"max_iterations": 5, "exit_when": "iteration >= 3 || previous.review.approved == true"}
  }
}
```

## 8. Replay

Two separate operations:

- `GET /v1/.../executions/{id}/replay` — **historical replay**: returns the execution, tasks, events and recorded
  outputs. It never re-executes an external operation.
- `POST /v1/.../executions/{id}:replay` — **re-execution**: creates a new execution with prior input or a supplied
  input and an explicit `reuse_outputs` list. Reused node outputs are seeded as succeeded; every other node runs
  normally. The new execution records `replay_of_execution_id`.

Requires `Idempotency-Key`; prior outputs are only reused when explicitly named.

## 9. Recovery and failure handling

- Control-node completion, failures and deferred state are committed in the dispatcher transaction.
- A crash after a control decision but before commit leaves the task pending and is re-evaluated on restart.
- A worker crash on any graph branch follows the Phase 08 lease/retry/checkpoint path. The rest of the graph stays
  untouched; join semantics decide what the downstream needs.
- Child executions are durable; polling resumes after restart. Parent cancellation cascades.
- A node input/schema error is a terminal task failure with a stable code.
- `EDGE_EVALUATION_FAILED`, `DEPENDENCY_FAILED`, `INPUT_SCHEMA_INVALID`, `INPUT_MAPPING_INVALID`,
  `NODE_EXECUTION_FAILED` and `SUBWORKFLOW_FAILED` are additive failure codes.

## 10. Verification

`TestGraphEngineTenNodeWorkflow` builds a 12-node graph with:

```
start → research → parallel → {web, db, file} → join → analysis → quality
quality --score>=80--> approve --\
quality --score<80 ---> revise  ---→ end
```

It asserts all control and worker tasks, both conditional branches, edge data mapping into the agent input, a
worker crash during `research`, a recovered retry and final success.

`TestGraphSubworkflowLoopApprovalDelayReplay` covers:

- a subworkflow with one child execution;
- a bounded loop with two children and `exit_when: iteration >= 2`;
- an approval node resolved through the API;
- a durable one-second delay;
- historical replay and re-execution with a seeded prior output.

Unit tests cover the validator (new node types, CEL errors, cycles, start constraints) and the executor registry
(condition/transform/delay/approval and worker routing).

## 11. Deliberate deviations

| Item | Status | Reason / trigger |
|---|---|---|
| Arbitrary same-graph back edges | Rejected | Bounded loops use child executions; unbounded graph cycles are unsafe and make state accounting ambiguous. |
| Human approval timeout | Not enforced | A timeout policy needs an operator product decision; the task remains pending until decided or the execution is cancelled. |
| Large output artifact materialisation | Reference-only | Inline outputs are bounded at 64 KiB; large artifacts stay references rather than being copied through every node. |
| Deterministic replay of side effects | Not attempted | Historical replay never re-executes; re-execution seeds only outputs explicitly chosen by an operator. |
| Parallel branch scheduling priority | Edge priority is stored and used for deterministic mapping order, not as a worker scheduling preemption guarantee. | Worker fairness remains Phase 06 policy. |
| Transform language | CEL only | The orchestrator never evaluates arbitrary Go/Python. |
| Approval/delay UI | API only | Dashboard wiring is a later phase. |
