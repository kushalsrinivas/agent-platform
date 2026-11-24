# Debugging, recovery and security

## Debugging

Start from the execution:

```bash
agentctl executions inspect exec_...
agentctl executions logs exec_...
```

The event stream answers what happened and when. The task list answers which node is stuck. The attempt rows
answer which worker ran, which lease owned it, and whether the result was schema-valid. Inline results and
checkpoint availability are shown without exposing credentials.

Useful API fields:

- `execution.counts` — terminal/non-terminal task counts.
- `execution.failure_code`, `failure_detail` — top-level failure.
- `task.state`, `task.skip_reason`, `task.attempt_count` — routing and retries.
- `attempt.error_code`, `outcome_unknown`, `effect_uncertain` — failure classification and side-effect risk.
- `event.reason` — `approval_requested`, `approval_vote`, `lease_expired`, `dependency_failed`, etc.

## Common cases

| Symptom | Check |
|---|---|
| execution stays `pending` | Does a worker in the node's pool have a free slot? `agentctl workers list`. |
| `NO_TASKS_STARTED` | Graph has no schedulable worker/control task. |
| `RESULT_SCHEMA_INVALID` | Agent output does not satisfy `output_schema`. |
| `EDGE_EVALUATION_FAILED` | CEL condition references a missing output or has a type error. |
| `DEPENDENCY_FAILED` | A join or branch failed; inspect skipped tasks. |
| `EXECUTION_TIMEOUT` | Execution deadline reached. Increase or remove the deadline. |
| approval never finishes | Check approvers/policy and `expires_at`; `agentctl approvals list --status pending`. |
| MCP tool fails | Inspect `mcp_tool_call` logs and the server's stderr; verify auth ref and tool policy. |

## Recovery

- Worker crash: leases expire, the reaper records `lease_expired`, and the task is retried within its policy.
- Orchestrator restart: state is in PostgreSQL; dispatch and recovery resume from committed rows.
- Checkpoints: resumable agents restore after the last model/tool boundary.
- Cancelled execution: `resume` creates a new execution and reuses successful node outputs.
- Approval expiry: the execution follows `on_reject` (`fail`, `skip` or `continue`).
- MCP crash: the tool call returns a normalised error; retry or fail according to the agent loop.

## Security

- Use API keys scoped to one project for CI and local tools. Project-scoped keys cannot read another project or
  create projects.
- Revoke keys immediately; the verifier checks the key row on every request.
- Never put provider/MCP secrets in an agent spec. Use `credential_ref` and the platform secret store.
- Tool policies are enforced at the runtime boundary; `requires_approval` fails closed.
- A waiting approval or delay does not occupy a worker slot.
- Execution events exclude credentials, secrets and raw provider responses.
- API errors return 404 for cross-tenant IDs to avoid existence leaks.
- Network egress is controlled by the worker/network policy; MCP HTTP requires `permissions.network`.
