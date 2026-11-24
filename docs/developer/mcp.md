# MCP

The Model Context Protocol is pinned in the agent version. The runtime connects, discovers tools and invokes them
inside the existing task lease.

## stdio

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

The process is owned by the runtime attempt and is terminated when the attempt ends. It inherits the runtime
environment plus any configured `env` map. Do not put secrets in `env`; use `auth_credential_ref`.

## Streamable HTTP

```json
{
  "name": "remote",
  "transport": "streamable_http",
  "url": "https://mcp.example.com/mcp",
  "timeout_seconds": 30,
  "auth_credential_ref": "remote-mcp"
}
```

The client sends JSON-RPC with `Accept: application/json, text/event-stream`, the negotiated protocol version,
an optional bearer token and `Mcp-Session-Id` when the server supplies one. JSON and SSE responses are accepted.

## Authentication

`auth_credential_ref` resolves from the attempt credential set. Local development may use
`MCP_CREDENTIAL_<REF>`; never commit that value. The runtime never logs the token.

## Tool policies

MCP tools inherit `MCP_DEFAULT_POLICY` (read-only, network allowed) unless the server declares a per-tool policy.
A tool advertising `destructiveHint` is denied unless `permissions.destructive` is true. A non-read-only tool is
denied unless `permissions.write` is true. Every MCP call requires `permissions.network`.

## Discovery and tracing

The runtime calls `initialize`, `notifications/initialized` and `tools/list` once per attempt. `tools/call`
results are normalised:

- text blocks are capped at 1 MiB; image bytes are described, not inlined;
- `structuredContent` is preserved;
- `isError` becomes a `ToolResult` error, not a runtime crash;
- every call emits an `mcp_tool_call` log record with server, tool, duration and outcome.

## View the registry

```bash
agentctl tools list
curl -H "X-API-Key: $AGENT_PLATFORM_API_KEY" \
  "$AGENT_PLATFORM_URL/api/v1/organizations/$ORG/projects/$PROJECT/mcp/servers"
```

The registry view aggregates servers pinned by published agent versions. It omits environment values and
credentials.
