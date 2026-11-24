# Quick start

This guide takes an empty project to a deployed agent and a completed workflow using only the public API.
You need an API URL, an organisation id, a project id and an API key.

## Install

```bash
pip install agent-platform-sdk
# or from this repository
pip install ./sdk/python
```

The Python package is `platform_sdk`. The older `agent_platform` health client remains available for
compatibility.

## Configure

```bash
export AGENT_PLATFORM_URL=https://api.example.com
export AGENT_PLATFORM_API_KEY=apk_...
export AGENT_PLATFORM_ORG=org_...
export AGENT_PLATFORM_PROJECT=proj_...
```

API keys are created by an organisation owner:

```bash
curl -X POST "$AGENT_PLATFORM_URL/api/v1/organizations/$AGENT_PLATFORM_ORG/api-keys" \
  -H "Authorization: Bearer $OIDC_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-laptop","project_id":"'$AGENT_PLATFORM_PROJECT'","role":"developer"}'
```

The response contains `key` once. Store it in your secret manager or `~/.config/agentctl/config.json`,
never in source control.

## Deploy an agent

```python
from platform_sdk import Client

spec = {
    "model": {"provider": "echo", "name": "echo-1", "credential_ref": "default"},
    "instructions": "Answer the user's question.",
    "runtime": {"abi_version": 1, "image_digest": "sha256:" + "a" * 64},
    "limits": {"max_turns": 4, "timeout_seconds": 120},
    "checkpoint": {"mode": "on_node_end", "require_checkpoint": False},
}

with Client() as client:
    agent = client.agents.create("hello-agent", spec)
    client.agents.deploy(agent.id, agent.latest_version)
    print(agent.id)
```

`echo` is a deterministic offline provider. For a real model, set `provider`, `name` and
`credential_ref` to a provider credential configured for the project.

## Deploy and run a workflow

```python
graph = {
    "nodes": [
        {"id": "start", "type": "start"},
        {"id": "answer", "type": "agent",
         "agent": {"agent_id": agent.id, "version": 1},
         "worker_pool": "default", "timeout_seconds": 120},
        {"id": "end", "type": "end"},
    ],
    "edges": [
        {"from": "start", "to": "answer"},
        {"from": "answer", "to": "end"},
    ],
    "input_schema": {"type": "object", "required": ["topic"]},
}

with Client() as client:
    workflow = client.workflows.create("hello-flow", graph)
    client.workflows.deploy(workflow.id, workflow.latest_version)
    execution = client.workflows.execute(workflow.id, workflow.latest_version, {"topic": "durable execution"})
    final = client.executions.wait(execution.id)
    print(final.state)
```

The execution is durable: the API returns immediately, the orchestrator schedules tasks, and workers report
fenced results. A worker crash does not lose the execution.

## CLI route

```bash
agentctl login --api-url "$AGENT_PLATFORM_URL" --api-key "$AGENT_PLATFORM_API_KEY"
agentctl project list
agentctl agent create --file agent.json
agentctl agent deploy --agent agt_...
agentctl workflow create --file workflow.json --deploy
agentctl workflow run --workflow wf_... --input input.json --follow
```

`agentctl init` writes a working manifest and templates. See [CLI](cli.md).
