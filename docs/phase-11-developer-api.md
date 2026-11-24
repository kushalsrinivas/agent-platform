# Phase 11 — Developer API, SDKs and CLI

Status: complete. The platform is usable from an empty project with an API key, the Python or Go SDK, or
`agentctl`, without touching PostgreSQL, NATS, workers or the orchestrator directly.

The exit criterion is tested twice against the live stack:

- `TestDeveloperAPIKeySDKExitCriterion` creates an API key, then uses the **Go SDK** to deploy an agent and
  execute a workflow.
- `TestPythonSDKLiveExitCriterion` runs `sdk/python/tests/live_exit.py`, which uses the **Python SDK** to create
  and deploy an agent, deploy a workflow, execute it, stream SSE events and wait for success.

## 1. Public API

`/api/v1` is canonical. `/v1` remains a compatibility alias. `/api/v1/organizations` aliases `/v1/orgs`.
The existing nested resource hierarchy is retained:

```
/api/v1/organizations/{org}/
    projects/{project}/
        environments/
        tools/
        mcp/servers/
        agents/
        workflows/ and workflows:validate
        executions/ :cancel :resume :replay tasks events stream approvals delegations
    workers/
    approvals/
    usage/
    api-keys/
```

Existing behavior is preserved: cursor pagination, consistent error envelopes, request validation, rate limits,
idempotency keys and the OpenAPI document. Execution listing now also accepts `created_after`, `created_before`
and `sort=created_at_desc|created_at_asc`.

## 2. API keys

`control.api_keys` stores a SHA-256 hash, prefix, role, optional project scope and revocation time. The plaintext
`apk_…` value is returned once.

- `POST /api/v1/organizations/{org}/api-keys` — owner only; project-scoped keys cannot have the owner role.
- `GET /api/v1/organizations/{org}/api-keys` — metadata only.
- `DELETE /api/v1/organizations/{org}/api-keys/{id}` — immediate revocation.

Verification happens before tenant resolution through the security-definer `control.resolve_api_key` function.
The key becomes a principal `apikey_<id>` with a membership row. A project-scoped key is rejected outside its
project, including query-based usage/approval endpoints. The verifier updates `last_used_at`.

## 3. Developer resources

- **Environments:** project-scoped development/staging/production labels with immutable kind and unique slug.
- **Workers:** fleet capacity, status, heartbeat, runtime and resource usage.
- **Tools:** built-in runtime tool catalog.
- **MCP:** servers pinned by published agent versions, aggregated by name, without secrets.
- **Usage:** execution/attempt/token/duration totals by project, with an RFC3339 window.
- **Approvals:** organisation-wide approval list with project/status filters.
- **Workflow validation:** `POST .../workflows:validate` compiles and validates a graph without storing it.
- **Execution resume:** `POST .../executions/{id}:resume` starts a new execution for a failed/cancelled one,
  reusing every node output that succeeded.
- **SSE:** `GET .../executions/{id}/stream` is now implemented (it was documented in Phase 09 but not mounted).
  It replays from `after`/`Last-Event-ID`, sends heartbeats and closes after a terminal execution's events.

## 4. Python SDK

`sdk/python/src/platform_sdk` is a new production SDK. The older `agent_platform` health client remains for
compatibility.

| Module | Contents |
|---|---|
| `client.py` | Sync `Client` and `AsyncClient`; auth, pooling, retries, error mapping |
| `agents.py` | create/get/list/versions/deploy |
| `workflows.py` | create/get/list/validate/versions/deploy/execute |
| `executions.py` | get/list/cancel/resume/wait/events/SSE/tasks/approvals/delegations |
| `tools.py` | catalog |
| `approvals.py` | list/for_execution/approve/reject/signal |
| `usage.py` | usage/workers/MCP |
| `keys.py`, `environments.py` | API keys and environments |
| `models.py` | typed pydantic response models |
| `exceptions.py` | status-to-exception hierarchy |

Safe retries cover `GET` and requests with an idempotency key. Timeouts, rate limits, pagination, typed models and
SSE are implemented. `AGENT_PLATFORM_*` environment variables provide defaults.

## 5. Go SDK

`sdk/go/client` now mirrors the resource surface: organizations, projects, environments, agents, workflows,
executions, tools, workers, usage, approvals, API keys, validation and SSE. It shares the documented JSON contract
and has an httptest contract test plus live integration coverage.

## 6. CLI

`agentctl` is built by `make build-go` into `bin/agentctl`.

```
agentctl login
agentctl init
agentctl project create/use/list
agentctl agent create/deploy
agentctl workflow create/validate/deploy/run
agentctl executions list/inspect/logs
agentctl workers list
agentctl tools list
agentctl approvals list
agentctl usage
agentctl dev
```

Config lives at `~/.config/agentctl/config.json` (0600), overridable with `AGENTCTL_CONFIG` and environment
variables. `dev` validates the workflow, optionally starts a local worker process, submits the workflow, streams
events and reports the final state. `--no-worker` attaches to an existing worker.

## 7. Local development mode

`agentctl dev`:

- loads the workflow JSON and validates it through the API;
- starts `bin/worker` with a unique worker id, the configured pool, NATS/orchestrator addresses and runtime
  command;
- supports `--no-worker` for an existing dev worker;
- streams live execution events and exits non-zero on failure;
- uses the normal worker environment for local variables and debugging.

The workflow's `worker_pool` must match the dev pool (`default` in the generated template).

## 8. Examples and documentation

- Ten runnable examples in `examples/python`: simple LLM, research, multi-agent, parallel, conditional, MCP,
  approval, scheduled, long-running and recovery.
- Developer documentation site config in `mkdocs.yml` with pages for quick start, installation, authentication,
  agents, workflows, lifecycle, SDKs, CLI, MCP, deployments, debugging, recovery, security, API and examples.
- Phase 11 implementation record (this document).

## 9. Verification

| Gate | Result |
|---|---|
| Go SDK live exit criterion with API key | Pass |
| Python SDK live exit criterion with API key and SSE | Pass |
| Project-scoped key isolation and revocation | Pass |
| Workflow validate endpoint | Pass |
| Execution resume endpoint | Pass |
| SSE stream endpoint | Pass |
| CLI login/init/project/agent/workflow/run/list/inspect/logs/tools/workers/usage/dev | Pass against the live stack |
| Full `make integration-test` | **Pass**, 167.8s |
| Ten Python SDK examples | Pass against the live stack, including local-worker MCP |
| Go race tests, golangci-lint, Ruff, mypy, OpenAPI, MkDocs strict | Pass |
| Clean migration apply + `0005` down | Pass |
| TypeScript/dashboard | Not changed |

## 10. Deliberate limits

- API keys are hashed but not encrypted at rest; the plaintext is shown once and never stored.
- `agentctl dev` starts a worker against the configured local platform; it does not embed a standalone
  orchestrator/database/NATS.
- Environments are labels/metadata; they do not yet select different runtime configuration.
- Usage aggregates inline attempt metrics; artifact-based usage accounting remains deferred.
- The documentation site is source and MkDocs configuration; it is not deployed by this repository.
- The Python SDK's live exit test requires the local stack and a worker in the `default` pool.
