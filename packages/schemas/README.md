# Shared JSON Schemas

Draft 2020-12 schemas used by Go and Python tests. They are the structural contract; semantic rules (DAG reachability,
retry safety, lease/result rules) live in the typed models.

| File | Validates |
|---|---|
| `workflow.schema.json` | Versioned workflow authoring document (`schema_version: 1`). |
| `task-dispatch.schema.json` | Self-contained attempt descriptor sent to a worker. |
| `task-result.schema.json` | Fenced worker result, including artifacts/checkpoint metadata. |
| `events/execution.created.schema.json` | `execution.created` payload. |
| `events/execution.state_changed.schema.json` | `execution.state_changed` payload. |
| `events/task.state_changed.schema.json` | `task.state_changed` payload. |
| `events/task.attempt_finished.schema.json` | `task.attempt_finished` payload. |
| `events/task.dispatch.schema.json` | `task.dispatch` payload. |

The message envelope is normative in [`docs/api/envelope.schema.json`](../../docs/api/envelope.schema.json) and is
not duplicated here. Event tests validate the envelope and the selected payload schema separately.

Requirements are described in [`docs/phase-03-contracts.md`](../../docs/phase-03-contracts.md). Changes are
additive within `schema_version: 1`; a breaking payload change increments the message type's version and requires a
migration window.
