# API reference

The normative OpenAPI document is `docs/api/openapi.yaml` in the repository.

## Base URL and versioning

`/api/v1` is canonical. `/v1` is a compatibility alias with the same resources. `/api/v1/organizations` is an
alias for `/v1/orgs`. Additive changes stay in v1; breaking changes move to v2.

## Resource groups

| Group | Paths |
|---|---|
| Organizations | `/api/v1/organizations`, `/api/v1/organizations/{org}` |
| Projects | `.../projects`, `.../projects/{project}` |
| Environments | `.../projects/{project}/environments` |
| Agents | `.../agents`, versions and `:publish` |
| Workflows | `.../workflows`, `.../workflows:validate`, versions and `:publish` |
| Executions | `.../executions`, `:cancel`, `:resume`, `:replay`, tasks, events, stream, approvals, delegations |
| Workers | `/api/v1/organizations/{org}/workers` |
| Tools | `.../projects/{project}/tools` |
| MCP | `.../projects/{project}/mcp/servers` |
| Approvals | `/api/v1/organizations/{org}/approvals` and execution approval rows |
| Usage | `/api/v1/organizations/{org}/usage` |
| API keys | `/api/v1/organizations/{org}/api-keys` |

## Authentication

- OIDC JWT: `Authorization: Bearer <token>`.
- API key: `X-API-Key: apk_...` or `Authorization: Bearer apk_...`.
- Development: `POST /v1/dev/token` (only when `AUTH_MODE=dev`).

API keys carry a role and optional project scope. Project-scoped keys are rejected outside their project.

## Errors

```json
{
  "error": {
    "code": "validation_failed",
    "message": "workflow graph is invalid",
    "details": {"problems": "..."},
    "request_id": "..."
  }
}
```

| Status | Typical code |
|---|---|
| 400 | `invalid_request` |
| 401 | `unauthorized` |
| 403 | `forbidden` |
| 404 | `not_found` |
| 409 | `conflict`, `idempotency_conflict` |
| 422 | `validation_failed`, `invalid_graph`, `invalid_cursor` |
| 429 | `rate_limited` / `quota_exceeded` |
| 5xx | `internal`, `orchestrator_unavailable` |

## Pagination

List endpoints accept `limit` and an opaque `cursor`; responses contain `items` and `next_cursor`.
The Python SDK exposes `Page` and `iter_all`; the Go SDK exposes `Page[T]`.

## Filtering and sorting

Executions accept:

- `state`
- `workflow_id`
- `created_after`, `created_before` (RFC3339)
- `sort=created_at_desc` (default) or `created_at_asc`
- `limit`, `cursor`

## Idempotency

Execution submission and resume require `Idempotency-Key` (8–128 visible ASCII characters). The same key and body
returns the original execution. A different body with the same key returns `409 idempotency_conflict`.

## Rate limits

Authenticated requests are rate-limited per caller. The API returns `429` and `Retry-After`; both SDKs retry safe
requests automatically.

## Streaming

`GET .../executions/{id}/stream` returns `text/event-stream`:

```
id: 7
event: task.state_changed
data: {"seq":7,...}
```

Send `Last-Event-ID` or `?after=7` to resume. The stream closes when the execution is terminal and all events are
delivered.

## Running an agent end to end

The dashboard uses the same public API described here; it has no private channel to the model or the worker.
For a one-node agent workflow:

```bash
API=http://localhost:8080/api/v1
ORG=org_...
PROJECT=proj_...
AGENT=agt_...
# Set AGENT_PLATFORM_API_KEY in the environment.

# 1. Publish the immutable agent version (once per version).
curl -X POST "$API/orgs/$ORG/projects/$PROJECT/agents/$AGENT/versions/1:publish" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY"

# 2. Create the run workflow and publish version 1 (once per agent version).
WORKFLOW=$(curl -sS -X POST "$API/orgs/$ORG/projects/$PROJECT/workflows" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "agent-run-'"$AGENT"'-v1",
    "version": {"graph": {
      "nodes": [{"id": "agent", "type": "agent",
                 "agent": {"agent_id": "'"$AGENT"'", "version": 1},
                 "worker_pool": "default", "timeout_seconds": 300}],
      "edges": [],
      "input_schema": {"type": "object", "properties": {"message": {"type": "string"}}}
    }}
  }' | jq -r .id)

curl -X POST "$API/orgs/$ORG/projects/$PROJECT/workflows/$WORKFLOW/versions/1:publish" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY"

# 3. Submit one message. 202 means durably accepted, not finished.
EXEC=$(curl -sS -X POST "$API/orgs/$ORG/projects/$PROJECT/executions" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: agent-run-$(date +%s)" \
  -d '{"workflow_id": "'"$WORKFLOW"'", "workflow_version": 1,
       "input": {"message": "User: Summarise the latest UK news."}}' | jq -r .id)

# 4. Poll until the execution is terminal.
curl "$API/orgs/$ORG/projects/$PROJECT/executions/$EXEC" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY" | jq .state

# 5. Read the node output.
curl "$API/orgs/$ORG/projects/$PROJECT/executions/$EXEC/replay" \
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY" | jq -r '.outputs.agent.report'
```

`GET .../executions/{id}/tasks/{task_id}` returns the same result as `result.output.report` plus attempt metrics.
The execution continues in the orchestrator and workers if the client disconnects; re-open the execution or call the
endpoints again to resume reading. The dashboard's **Run this agent** panel stores the pending execution id in the
browser, so navigating away and back resumes the same run.

## SDKs

- Python: [`platform_sdk`](sdk.md)
- Go: `sdk/go/client`
