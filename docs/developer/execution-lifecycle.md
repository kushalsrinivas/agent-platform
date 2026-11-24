# Execution lifecycle

## States

`pending → running → succeeded | failed | cancelled`

`failing` and `cancel_requested` are transitional states while pending work drains. Terminal states are final.

## Submit

```python
execution = client.workflows.execute(workflow_id, version, {"topic": "..."})
```

Submission requires an idempotency key; the SDK generates one. Repeating the same key and body returns the
original execution, never a duplicate.

## Follow

Poll:

```python
final = client.executions.wait(execution.id, timeout=300)
```

Stream:

```python
for event in client.executions.stream_events(execution.id):
    print(event.seq, event.event_type, event.reason)
```

The SSE stream sends `id`, `event` and the full JSON event in `data`. It closes after all events of a terminal
execution. Reconnect with `Last-Event-ID` or `after`.

## Inspect

- `client.executions.get(id)` — counts, state, failure code.
- `client.executions.tasks(id)` — node states and attempts.
- `client.executions.task(id, task_id)` — attempts, error codes, inline result/checkpoint availability.
- `client.executions.events(id, after=...)` — historical audit.
- `client.executions.approvals(id)` — approval rows and votes.
- `client.executions.delegations(id)` — supervisor-to-child hierarchy.

## Cancel

```python
client.executions.cancel(execution.id, reason="operator request")
```

Cancellation is best effort for side effects. Pending tasks are cancelled; running tasks are fenced when their
lease expires. The execution finalises as `cancelled`.

## Resume

```python
resumed = client.executions.resume(execution.id)
```

Resume is allowed for `failed` or `cancelled` executions. It creates a new execution with the same pinned workflow
and input, reusing every node whose output succeeded. Only missing/failed nodes run. The original execution is
never mutated.

## Replay

`GET .../replay` is read-only: it returns the historical execution, tasks, outputs and events without re-running.
`POST .../replay` creates a new execution with explicitly selected `reuse_outputs`.

## Approvals

A waiting approval does not occupy a worker slot. Approve or reject the task:

```python
approvals = client.executions.approvals(execution.id)
client.approvals.approve(execution.id, approvals[0].task_id, comment="looks good")
```

Policy `any` decides on the first vote. `all` waits for every assigned approver; any rejection decides immediately.
Expiry is evaluated by the orchestrator and audited as an `expire` decision.
