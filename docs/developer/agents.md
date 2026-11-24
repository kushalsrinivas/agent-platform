# Agents

An agent version is immutable. `create` stores version 1 as a draft; `deploy` publishes it. Executions pin the
exact version id.

```json
{
  "model": {"provider": "openai", "name": "gpt-4.1-mini", "credential_ref": "openai"},
  "instructions": "You are a careful research assistant.",
  "tools": [
    {"tool_id": "tool_web_search", "version": 1,
     "policy": {"permissions": {"read": true, "network": true},
                "timeout_seconds": 30, "max_retries": 1}}
  ],
  "runtime": {"abi_version": 1, "image_digest": "sha256:..."},
  "limits": {"max_turns": 8, "timeout_seconds": 300},
  "checkpoint": {"mode": "on_node_end", "require_checkpoint": false},
  "output_schema": {"type": "object", "required": ["report"]}
}
```

## Model

`provider`, `name` and `credential_ref` are pinned. `credential_ref` names a project credential; it is never
the secret value. Supported providers are the ones shipped in the runtime image (`echo`, `openai`, `anthropic`,
`gemini` and OpenAI-compatible endpoints).

## Tools

Bind built-in tools by id and version. Policies are enforced by the runtime:

- `permissions.read/write/destructive/network`
- `timeout_seconds`, `concurrency_limit`
- `max_retries`, `idempotency`
- `auth_scope`, `requires_approval`

`requires_approval` fails closed at the tool boundary. Put an approval node before the tool-using node.

## MCP

Add `mcp_servers` to the agent version. See [MCP](mcp.md).

## Supervisor agents

A supervisor may delegate to an exact list of agent versions:

```json
{
  "orchestration": {
    "allowed_agents": [
      {"agent_id": "agt_research", "version": 1},
      {"agent_id": "agt_analysis", "version": 1}
    ],
    "max_children": 8,
    "max_depth": 3,
    "max_parallel": 4,
    "max_total_tokens": 200000,
    "max_duration_seconds": 3600,
    "max_retries": 1
  }
}
```

The runtime exposes delegation tools only to supervisor agents. A supervisor waiting for children occupies one
worker slot; size the pool for `1 + max_parallel` or use a separate child pool.

## Output schemas

`output_schema` is enforced after the runtime returns. A mismatch fails the attempt with `RESULT_SCHEMA_INVALID`.
Use it to guarantee the shape downstream nodes receive.
