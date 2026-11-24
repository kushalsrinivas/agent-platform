# Python and Go SDKs

## Python

```bash
pip install agent-platform-sdk
```

```python
from platform_sdk import Client, AsyncClient

with Client(api_key=..., org_id=..., project_id=...) as client:
    agent = client.agents.create("researcher", spec)
    client.agents.deploy(agent.id, agent.latest_version)
    workflow = client.workflows.create("flow", graph)
    client.workflows.deploy(workflow.id, workflow.latest_version)
    execution = client.workflows.execute(workflow.id, workflow.latest_version, {"topic": "x"})
    final = client.executions.wait(execution.id)
```

Async:

```python
async with AsyncClient(api_key=..., org_id=..., project_id=...) as client:
    execution = await client.workflows.execute(workflow_id, 1, {"topic": "x"})
    final = await client.executions.wait(execution.id)
    async for event in client.executions.stream_events(execution.id):
        print(event.seq)
```

### Resource methods

| Resource | Methods |
|---|---|
| `organizations` | `create`, `list` |
| `projects` | `create`, `list`, `get` |
| `agents` | `create`, `get`, `list`, `iter_all`, `create_version`, `list_versions`, `get_version`, `deploy` |
| `workflows` | `create`, `get`, `list`, `iter_all`, `validate`, `create_version`, `list_versions`, `deploy`, `execute` |
| `executions` | `get`, `list`, `iter_all`, `cancel`, `resume`, `wait`, `events`, `stream_events`, `tasks`, `task`, `approvals`, `delegations` |
| `tools` | `list` |
| `approvals` | `list`, `for_execution`, `approve`, `reject`, `signal` |
| `usage` | `get`, `workers`, `mcp_servers` |
| `keys` | `create`, `list`, `revoke` |
| `environments` | `create`, `list`, `get` |

### Behaviour

- Automatic API-key or bearer auth from constructor or `AGENT_PLATFORM_*` environment variables.
- Connection pooling through one `httpx` client.
- Safe retries: `GET` and requests with an idempotency key retry `429`, `502`, `503` and `504` with backoff and
  `Retry-After`.
- Timeouts raise `PlatformTimeoutError`.
- Non-2xx responses map to `AuthenticationError`, `PermissionDeniedError`, `NotFoundError`, `ConflictError`,
  `ValidationError`, `RateLimitError`, `ServerError` or `APIError`.
- Typed pydantic models and cursor pagination (`Page`, `iter_all`).
- SSE streaming yields typed `Event` objects.

Set `AGENT_PLATFORM_URL`, `AGENT_PLATFORM_API_KEY`, `AGENT_PLATFORM_ORG` and `AGENT_PLATFORM_PROJECT` to avoid
passing configuration on every call.

## Go

```go
api := client.NewWithConfig(client.Config{
    BaseURL: os.Getenv("AGENT_PLATFORM_URL"),
    APIKey:  os.Getenv("AGENT_PLATFORM_API_KEY"),
    OrgID:   os.Getenv("AGENT_PLATFORM_ORG"),
    ProjectID: os.Getenv("AGENT_PLATFORM_PROJECT"),
})
agent, err := api.CreateAgent(ctx, "", "researcher", "", spec)
_, err = api.DeployAgent(ctx, "", agent.ID, 1)
workflow, err := api.CreateWorkflow(ctx, "", "flow", "", graph)
_, err = api.DeployWorkflow(ctx, "", workflow.ID, workflow.LatestVersion)
execution, err := api.ExecuteWorkflow(ctx, "", workflow.ID, workflow.LatestVersion, input,
    client.ExecuteOptions{})
final, err := api.WaitExecution(ctx, "", execution.ID, 5*time.Minute, time.Second)
```

The Go SDK has the same typed resources and error type. It shares the JSON contract, so a model change is a
compile error or a contract-test failure, not a silent divergence. Use `api.StreamEvents` for SSE.

Both SDKs are covered by the contract tests in `sdk/python/tests` and `sdk/go/client/sdk_test.go`, plus live
exit-criterion tests in `tests/integration`.
