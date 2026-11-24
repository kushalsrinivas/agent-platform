# Phase 10 — Multi-agent orchestration, delegation and MCP

Status: complete. Agents delegate through the orchestrator, never by calling another worker process directly.
Delegation is a durable child execution; the supervisor is an ordinary agent task whose runtime exposes
lease-scoped delegation tools.

The exit criterion is tested against the live stack: a supervisor delegates three research/analysis/coding tasks
concurrently, the coding child fails its output schema, a fallback agent succeeds, and the supervisor returns an
aggregated result (`TestSupervisorDelegatesConcurrentlyAndRecovers`).

## 1. Delegation architecture

```
supervisor agent (worker slot A)
        |
        | DelegateTask(task, attempt, lease)
        v
orchestrator  ── validates allowlist, depth, count, concurrency, tokens, deadline
        |
        ├── child execution (research)  ── worker slot B
        ├── child execution (analysis)  ── worker slot C
        └── child execution (coding)    ── worker slot D
                     |
                     └── fails
        supervisor re-delegates to fallback ── recovered result
```

- `WorkerControl.DelegateTask` creates a child execution plus one pinned agent task. The parent task id, attempt id
  and lease id are the only credentials the runtime holds; there is no database, NATS or object-store access.
- `GetDelegation` returns status, output, error and token usage. `ListDelegationAgents` returns the frozen
  allowlist and remaining budget.
- A delegation is idempotent per `(parent_task_id, idempotency_key)`, so a runtime replay from a checkpoint does not
  create duplicate children.
- `delegate_task` waits only when the caller asks. `delegate_tasks` starts all children concurrently, waits for them
  and can retry failed children on a fallback agent. Admission takes a transaction-scoped advisory lock on the root
  execution, so concurrent calls cannot each observe an under-budget tree.
- Worker consumers dispatch up to `max_concurrency` attempts concurrently (NATS `MaxAckPending` is the same bound).
  A supervisor waiting on children therefore does not block their delivery on the same worker process.
- A supervisor occupies one worker slot while it waits. A pool must therefore provide one slot for the supervisor
  plus `max_parallel` child slots, or separate pools for supervisors and children.

## 2. Parent-child tracking

`execution.executions.parent_execution_id`, `parent_task_id`, `iteration` and `delegation_depth` form the durable
hierarchy. `execution.delegations` adds the delegation-specific record:

| Column | Meaning |
|---|---|
| `id` | `dlg_…` delegation id |
| `root_execution_id` | top-level execution; budgets are enforced across the whole tree |
| `parent_execution_id`, `parent_task_id` | the supervisor attempt that asked |
| `child_execution_id` | the child execution |
| `agent_id`, `agent_version` | exact pinned assignment |
| `request` | task, context, context refs and requested output schema |
| `shared_context` | scoped context passed to the child |
| `context_refs` | artifact/memory references, not copied blobs |
| `status`, `result_inline`, `error_code`, `error_message` | terminal result |
| `idempotency_key` | stable replay protection |

`GET /v1/orgs/{org}/projects/{project}/executions/{id}/delegations` returns the hierarchy rows for one root
execution. The child execution remains a normal execution and is visible through the execution endpoints.

## 3. Delegation policies

The policy is part of the immutable supervisor agent version (`orchestration`):

```json
{
  "allowed_agents": [
    {"agent_id": "agt_...", "version": 1, "name": "research"},
    {"agent_id": "agt_...", "version": 1, "name": "analysis"}
  ],
  "max_children": 8,
  "max_depth": 3,
  "max_parallel": 4,
  "max_total_tokens": 200000,
  "max_duration_seconds": 3600,
  "max_retries": 1
}
```

- `allowed_agents` is an exact id + version allowlist; an unlisted or unpublished version is rejected.
- `max_children` counts the whole root hierarchy; `max_parallel` counts active children.
- `max_depth` is checked from the root execution; the schema also caps depth at 32.
- `max_total_tokens` sums child attempt `tokens_in + tokens_out` before admitting a new delegation.
- `max_duration_seconds` caps the child deadline, never extending the parent deadline.
- `max_retries` becomes the delegated task's max attempts (capped at 10).
- A child inherits neither the supervisor's allowlist nor its budget; its own agent version decides whether it may
  delegate further. This prevents an agent from granting itself more authority by being delegated to.

The runtime exposes `list_delegation_agents`, `delegate_task`, `delegate_tasks`, `get_delegation`,
`publish_agent_event` and `wait_agent_event` only when `orchestration` is present.

## 4. Shared context

Children never receive the parent's conversation history. A delegation carries only:

- the task-specific input (`task`);
- a scoped `context` object chosen by the supervisor;
- `context_refs` for artifact or memory references;
- output schema and timeout overrides;
- execution metadata injected by the orchestrator (parent id, depth, deadline).

The child execution stores only that input. Parent instructions and prior results must be summarised or referenced
explicitly by the supervisor; this is a correctness and privacy boundary, not a convenience.

## 5. Durable agent messaging

`execution.agent_events` is an append-only message table scoped to `root_execution_id`.

- `PublishAgentEvent(topic, payload)` writes one durable event.
- `WaitAgentEvent(topic, timeout)` long-polls with `FOR UPDATE SKIP LOCKED`, marks the event consumed by the
  execution and returns it. A wait never occupies a worker slot while no event exists, but the runtime process is
  still running; long waits should be bounded.
- Events are not a substitute for the request-response delegation path. Use delegation when the supervisor needs a
  result; use events for fan-in notifications, progress and coordination.

## 6. MCP subsystem

`runtimes/python/src/mcp/manager.py` implements the client runtime:

| Component | Implementation |
|---|---|
| Server registry | Immutable `mcp_servers` list in the agent version |
| Connection manager | One lazy connection per server, serialised by an `asyncio.Lock` |
| Transports | stdio (JSON-RPC over a child process) and Streamable HTTP |
| Authentication | `auth_credential_ref` resolved from attempt credentials or `MCP_CREDENTIAL_<REF>` (development) |
| Discovery | `initialize`, `notifications/initialized`, `tools/list` |
| Invocation | `tools/call` with timeout and error normalisation |
| Result normaliser | Text/image/resource blocks capped at 1 MiB per text block and 4 MiB per result |
| Execution tracing | `mcp_tool_call` log event with server, tool, duration and outcome |

Server configuration:

```json
{
  "name": "filesystem",
  "transport": "stdio",
  "command": "/usr/local/bin/mcp-filesystem",
  "args": ["/workspace"],
  "timeout_seconds": 30,
  "auth_credential_ref": "mcp-filesystem",
  "tool_policies": {
    "read_file": {"permissions": {"read": true, "network": false}}
  }
}
```

Streamable HTTP uses `Content-Type: application/json`, `Accept: application/json, text/event-stream`, the
negotiated protocol version, an optional bearer token and `Mcp-Session-Id` when the server supplies one. Both JSON
and SSE responses are accepted.

## 7. Tool execution policy

Every tool binding and MCP tool may declare:

```json
{
  "permissions": {"read": true, "write": false, "destructive": false, "network": false},
  "timeout_seconds": 30,
  "concurrency_limit": 1,
  "auth_scope": "mcp-filesystem",
  "max_retries": 1,
  "idempotency": "optional",
  "requires_approval": false
}
```

The runtime enforces the policy before invocation:

- network access is denied unless `permissions.network` is true (all MCP calls require it);
- an MCP tool advertising `destructiveHint` is denied unless `permissions.destructive` is true;
- a tool advertising `readOnlyHint: false` is denied unless `permissions.write` is true;
- `idempotency: required` rejects a call without a stable idempotency key;
- `requires_approval` returns `APPROVAL_REQUIRED`; wrap the call in an approval node rather than bypassing the gate;
- timeout, per-tool concurrency and bounded retries are enforced by `ToolExecutor`;
- an `auth_scope` on an MCP tool requires the server to have a resolvable credential.

Default policy is read-only and non-destructive. A tool that is not in the agent version cannot be called.

## 8. Human-in-the-loop approvals

Approval remains a durable control node, now with assignment and audit:

```json
{
  "id": "review",
  "type": "approval",
  "config": {
    "approval": {
      "prompt": "Ship release 2026.11?",
      "approvers": ["user-a", "user-b"],
      "policy": "all",
      "expires_in_seconds": 3600,
      "reject_reason_required": true,
      "on_reject": "fail"
    }
  }
}
```

- The first execution persists `execution.approvals` and a task `control_state`; the worker slot is released. A
  `task.state_changed` event with reason `approval_requested` is written to the audit stream and outbox.
- `policy: any` decides on the first vote. `policy: all` stays pending until every assigned approver approves; any
  rejection decides immediately.
- `approvers` is enforced against the acting principal. An empty list allows any operator with the required role.
- Every vote is written to `execution.approval_decisions` with actor, decision, comment and timestamp.
- Rejection reasons are required when configured. Approval expiry is evaluated by the control tick and recorded as
  an `expire` decision; `on_reject` decides whether the execution continues or fails.
- `GET /v1/orgs/{org}/projects/{project}/executions/{id}/approvals` returns the approval and decision audit trail.
- The existing `:approve`, `:reject` and `:signal` endpoints apply the decision. All transitions emit the standard
  execution events and outbox messages.

## 9. Verification

| Gate | Result |
|---|---|
| Supervisor delegates to 3 agents concurrently and aggregates | Pass (`TestSupervisorDelegatesConcurrentlyAndRecovers`) |
| One child fails and a fallback recovers it | Pass, 4 delegations, 3 succeeded, 1 failed, `recovered: 1` |
| Child executions overlap in time | Pass |
| MCP stdio discovery, invocation and destructive-tool denial | Pass (`TestMCPDiscoveryInvocationAndPolicy`) |
| MCP Streamable HTTP discovery and invocation | Pass (`test_streamable_http_discovery_and_call`) |
| Tool policy permissions, timeout, retry and idempotency | Pass (`test_tool_policy.py`) |
| Approval any/all, assignment, reason and audit | Pass (`TestApprovalAssignmentPolicyAuditAndExpiry`) |
| Approval expiry releases the execution | Pass (`TestApprovalExpiryReleasesExecution`) |
| Full integration suite, race tests, lint, mypy, OpenAPI | Recorded in the final Phase 10 report |

## 10. Deliberate limits

- Delegated children bypass the project active-execution quota; depth, child count, parallelism and loops remain
  bounded.
- `max_total_tokens` gates new delegations; it does not interrupt a child already running over budget.
- A supervisor holds its worker slot while waiting. Size the pool for supervisor + parallel children.
- MCP credentials resolve from attempt credentials or a development environment variable until the platform secret
  store is wired. Production should inject them through `GetTaskCredentials`.
- stdio MCP servers inherit the runtime process environment plus the configured `env`; secrets must use the auth
  credential seam, not the immutable agent spec.
- `requires_approval` fails closed at the tool boundary. Agent-initiated approval that resumes a blocked runtime
  process is not implemented; compose a workflow approval node before the tool-using node.
- Agent events are consumed at most once per consumer; there is no replay/ack window yet.
- Tool policies are declarations enforced at the runtime boundary, not a sandbox for arbitrary tenant code.
- LangGraph remains agent-internal. Platform routing, delegation, retries and approval state are Go-owned.
