# CLI

`agentctl` is built from `cli/cmd/agentctl`.

```bash
make build-go      # bin/agentctl
agentctl version
```

## Login and configuration

```bash
agentctl login --api-url https://api.example.com --api-key apk_...
# or read the key from stdin without putting it in shell history
agentctl login --api-url https://api.example.com --api-key -
```

The config is `~/.config/agentctl/config.json` (mode 0600). Override with `AGENTCTL_CONFIG`.
Environment variables `AGENT_PLATFORM_URL`, `AGENT_PLATFORM_API_KEY`, `AGENT_PLATFORM_TOKEN`,
`AGENT_PLATFORM_ORG` and `AGENT_PLATFORM_PROJECT` take precedence.

## Project and resources

```bash
agentctl project list
agentctl project create --name "My project" --slug my-project
agentctl project use --project proj_...
agentctl agent create --file agent.json
agentctl agent deploy --agent agt_... --version 1
agentctl workflow validate --file workflow.json
agentctl workflow create --file workflow.json --deploy
agentctl workflow deploy --workflow wf_... --version 1
agentctl workflow run --workflow wf_... --input input.json --follow
agentctl tools list
agentctl workers list --pool default
agentctl approvals list --status pending
agentctl usage --from 2026-11-01T00:00:00Z
```

`workflow run --follow` streams the execution events and exits non-zero when the final state is not `succeeded`.

## Inspect a run

```bash
agentctl executions list --state failed --limit 20
agentctl executions inspect exec_...
agentctl executions logs exec_... --after 0
```

`inspect` prints the execution and task rows. `logs` streams the same events as the API SSE endpoint. Redirect
to a file to keep a local debugging record.

## Local development mode

```bash
agentctl init --dir demo --name hello
cd demo
# create/choose a project, then create and deploy the agent
agentctl agent create --file agent.json
agentctl agent deploy --agent agt_...
# replace agt_REPLACE_WITH_AGENT_ID in workflow.json
agentctl workflow create --file workflow.json --deploy
agentctl dev --file workflow.json --input input.json
```

`agentctl dev` validates the workflow, starts a local worker process (unless `--no-worker`), submits the
workflow, streams events and prints the final state. The worker reads its normal environment:

- `WORKER_NATS_URL` (`NATSURL` in config, default `nats://localhost:4222`)
- `WORKER_ORCHESTRATOR_GRPC_ADDR` (default `localhost:9090`)
- `WORKER_RUNTIME_COMMAND` (default `python -m agent_runtime serve --socket {socket}`)
- `WORKER_POOL` (the `--pool` value)

`agentctl dev --no-worker` attaches to an already-running worker in the same pool. Use
`AGENTCTL_WORKER_BIN` or `--worker-bin` if the worker binary is not `bin/worker`. If the Python runtime
executable path contains spaces, quote it inside `WORKER_RUNTIME_COMMAND` (the worker splits the command with
quote support and does not invoke a shell).

The node `worker_pool` in the workflow must match the dev pool. The init template uses `default`.
