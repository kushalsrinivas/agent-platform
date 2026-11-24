# Deployments

## Versions are immutable

Creating an agent or workflow stores version 1 as a draft. Publishing freezes the content and makes it available
to executions. A later change creates a new version. A published workflow pins exact agent versions, so deploying
a new agent version does not silently change running or future executions of an older workflow.

## Deploy an agent

```python
agent = client.agents.create("researcher", spec)
client.agents.deploy(agent.id, agent.latest_version)
```

## Deploy a workflow

```python
workflow = client.workflows.create("research-flow", graph)
client.workflows.deploy(workflow.id, workflow.latest_version)
```

Validate first when the graph is generated dynamically:

```python
client.workflows.validate(graph)
```

## Execute a pinned version

```python
execution = client.workflows.execute(workflow.id, 3, input)
```

The execution stores `workflow_version_id` and every task stores the exact `agent_version_id`/`tool_version_id`.
Audit does not depend on a live join.

## Environments

Environments are project labels for development, staging or production:

```python
client.environments.create("Production", slug="production", kind="production")
```

They do not change scheduling; they provide a stable place to attach configuration and promotion metadata.
Executions remain isolated by project and version.

## Rollout

1. Publish the new agent version.
2. Publish a new workflow version that pins it.
3. Run a small execution and inspect events/tasks.
4. Submit broader traffic against the new workflow version.
5. Keep the old published workflow version for rollback; execution versions are immutable.

## CLI

```bash
agentctl agent deploy --agent agt_... --version 2
agentctl workflow create --file workflow.json --deploy
agentctl workflow run --workflow wf_... --version 2 --input input.json --follow
```
