# Agent Platform Python SDK

Sync and async typed clients for the Agent Platform developer API.

```bash
pip install agent-platform-sdk
```

```python
from platform_sdk import Client

with Client(api_key="apk_...", org_id="org_...", project_id="proj_...") as client:
    agent = client.agents.create("researcher", spec)
    client.agents.deploy(agent.id, agent.latest_version)
    workflow = client.workflows.create("brief", graph)
    client.workflows.deploy(workflow.id, workflow.latest_version)
    execution = client.workflows.execute(workflow.id, workflow.latest_version, {"topic": "durable execution"})
    final = client.executions.wait(execution.id)
    print(final.state)
```

Async is `from platform_sdk import AsyncClient`. Both clients support typed models, pagination, safe retries,
timeouts, exception mapping and SSE (`client.executions.stream_events(...)`).

Configuration can come from `AGENT_PLATFORM_URL`, `AGENT_PLATFORM_API_KEY`, `AGENT_PLATFORM_ORG` and
`AGENT_PLATFORM_PROJECT`.

The older `agent_platform` package remains as a small health/meta client for compatibility.
