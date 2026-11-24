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

## SDKs

- Python: [`platform_sdk`](sdk.md)
- Go: `sdk/go/client`
