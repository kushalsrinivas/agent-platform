# Phase 12 — Developer Dashboard and Execution Explorer

Status: complete. The dashboard is wired to the Phase 11 developer API and renders live projects, agents,
workflows, executions, events, approvals, workers and usage. Operators can watch a run, open the failed node,
read its input/output and error, and decide or recover without a server terminal.

## 1. Connection and scope

The browser calls the same-origin proxy at `/api/platform/*`, which forwards to `/api/v1/*` on the API
service. The proxy is the only place credentials are handled:

- `DASHBOARD_API_KEY` injects a fixed server-side `X-API-Key`.
- `DASHBOARD_TOKEN` injects a bearer token instead (OIDC or dev issuer).
- Without either, each request forwards the browser key as `X-Dashboard-Api-Key`, promoted to `X-API-Key`.

SSE responses are streamed through unchanged; the proxy disables caching and hop-by-hop headers. The selected
organization, project and environment, plus an optional browser API key, are stored in local storage through
the Zustand workspace store. Organization and project selectors are required; every query is scoped to them.

## 2. Pages

| Route | Purpose |
|---|---|
| `/` | Totals, active/queued/failed, success rate, duration, tokens, estimated cost, worker availability, recent runs and execution mix, with 1h/24h/7d/30d/custom ranges. |
| `/agents` and `/agents/{id}` | Catalog with model/version, configuration, tool and MCP permissions, version history, usage stats, execution history and a test run. |
| `/workflows` and `/workflows/{id}` | Catalog with success rate and duration, immutable graph, node configuration, deployment/version history, trigger settings and execution history. |
| `/executions` | Filterable, searchable execution history with a new-execution dialog for published workflows. |
| `/executions/{id}` | Live graph, event timeline, node inspector, model invocation and attempt metrics, failure inspector, logs and recovery actions. |
| `/approvals` | Pending and historical human decisions, with approve/reject from the dashboard. |
| `/workers`, `/tools`, `/mcp`, `/usage`, `/settings` | Fleet capacity, tool schemas, MCP bindings, spend and workspace diagnostics. |

The execution explorer selects the first failed task, otherwise a running task, otherwise an agent node. The
node inspector shows the pinned node configuration, upstream outputs (derived from the durable replay outputs),
the execution input, the task result, per-attempt errors and attempt history. The failure inspector shows the
top-level failure code, failed nodes, retry history and related events, with cancel and resume actions.

## 3. Live updates

`useExecutionStream` consumes `GET .../executions/{id}/stream` and reconnects with the last persisted event
sequence (`?after=<seq>`). Events are merged into the TanStack Query events cache by `seq`, then execution and
task queries are invalidated with a short debounce. The stream is disabled for terminal executions; a dropped
connection shows `reconnecting` and resumes from durable history rather than losing events.

Persisted history remains available through `/events?after=<seq>`. The logs tab filters by event type, node
type, free text and event/entity id and renders redacted raw JSON.

## 4. Redaction and safety

`src/lib/redact.ts` masks credential-shaped keys (`token`, `secret`, `password`, `api_key`, `authorization`,
`credential`, `cookie`) and inline `apk_`/bearer values before any diagnostic JSON is rendered. Secrets are never
displayed in selectors or menus. The dashboard makes no state changes except explicit operator actions:
new execution, agent test, approval decision, cancel and resume.

## 5. Backend additions

Phase 12 adds two small read APIs needed by the explorer:

- `GET /api/v1/.../executions/{id}/input` — the immutable submitted input.
- `Attempt.metrics` on task details — runtime token/duration metrics already stored by the agent runtime.

`GetTask` now also returns the latest attempt result (outcome, output, error code/message) and includes the
missing priority column in its scan.

## 6. Known limits

- Node inputs are reconstructed from upstream replay outputs; per-node request/response bodies are not stored.
- Worker process logs and stack traces are not persisted. The durable record is the attempt error, events and
  outputs.
- Usage and cost are aggregated from attempts; pricing is an estimate (`$0.15` / `$0.60` per million input/output
  tokens) and not an invoice.
- Search and catalog statistics use the latest 100–200 project executions rather than a dedicated analytics
  endpoint.

## 7. Configuration

`docker-compose.yml` sets `DASHBOARD_API_URL=http://api:8080` for the dashboard and passes
`DASHBOARD_API_KEY` / `DASHBOARD_TOKEN` from `.env` when present. Without a server key, connect with an
`apk_…` project-scoped operator key from the dashboard dialog or `/settings`.
