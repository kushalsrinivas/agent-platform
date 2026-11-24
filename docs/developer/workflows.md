# Workflows

A workflow is a versioned DAG. Publishing freezes the graph. Executions pin one published version.

## Nodes

| Type | Purpose |
|---|---|
| `start`, `end` | Entry and termination. Optional; roots/terminals are derived when absent. |
| `agent`, `llm` | Run a pinned agent in the Python runtime. |
| `tool` | Run a pinned built-in tool. |
| `condition` | Evaluate a CEL expression and store `{"result": bool}`. |
| `transform` | Evaluate a CEL mapping into a structured output. |
| `parallel` | Fan-out marker. Branches are ordinary nodes. |
| `join` | Fan-in with `all` or `successful N`. |
| `delay` | Durable wait (`seconds`, `until` or external `event`). |
| `approval` | Human decision with approvers, policy, expiry and audit. |
| `subworkflow` | Run a pinned child workflow. |
| `loop` | Bounded child-workflow repetition with a CEL exit condition. |

## Validate before storing

```python
result = client.workflows.validate(graph)
assert result.valid
```

The same validator runs at create/publish. It checks unique ids, edges, entry/termination, reachability,
configuration, CEL compilation, schemas, join thresholds, resource limits and cycles. Cycles are rejected;
use a bounded `loop` node.

## Data flow

The runtime receives:

```json
{
  "input": {"...": "execution input"},
  "outputs": {"previous_node": {"...": "output"}},
  "node_input": {"...": "mapped input"},
  "params": {"...": "node params"}
}
```

- `input_from` limits which direct upstream outputs are passed to a node. Without it, active inbound edges are
  used, not every ancestor.
- Edge `data_mapping` maps dotted target paths to CEL expressions.
- `input_schema` validates the resolved node input before execution.
- `output_schema` validates the result before it becomes visible downstream.

## Conditional routing

```json
{"from": "score", "to": "premium", "when": "outputs.score.result == true"}
```

Conditions are CEL, compiled at publish time. Arbitrary Go/Python is never evaluated.

## Parallel and join

```json
{"id": "fan", "type": "parallel"}
{"id": "join", "type": "join", "config": {"join": {"mode": "successful", "successful": 1, "on_partial": "skip"}}}
```

A fan-in must use a `join` node. `all` waits for every active branch; `successful N` can activate before every
branch is terminal.

## Delays and approvals

Delay and approval nodes stay `pending` with persisted control state. No worker slot is held. Approval waits
until a decision or expiry; a delay waits until its time or external signal.

## Retries

Node `retry.max_attempts` and `retry_on` are enforced by the orchestrator. Prefer idempotent tools and stable
idempotency keys for side-effecting nodes.
