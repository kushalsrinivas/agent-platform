"""Shared helpers for the Python SDK examples."""

from __future__ import annotations

import os
import sys

from platform_sdk import Agent, Client, Execution


def api() -> Client:
    return Client(
        base_url=os.environ.get("AGENT_PLATFORM_URL", "http://localhost:8080"),
        api_key=os.environ.get("AGENT_PLATFORM_API_KEY"),
        org_id=os.environ.get("AGENT_PLATFORM_ORG"),
        project_id=os.environ.get("AGENT_PLATFORM_PROJECT"),
        timeout=120,
    )


def agent_spec(instructions: str, *, tools: list[dict] | None = None,
               orchestration: dict | None = None,
               mcp_servers: list[dict] | None = None,
               output_schema: dict | None = None) -> dict:
    provider = os.environ.get("MODEL_PROVIDER", "echo")
    model_name = os.environ.get("MODEL_NAME", "echo-1" if provider == "echo" else "gpt-4.1-mini")
    digest = os.environ.get("RUNTIME_DIGEST", "sha256:" + "a" * 64)
    spec: dict = {
        "model": {
            "provider": provider,
            "name": model_name,
            "credential_ref": os.environ.get("MODEL_CREDENTIAL", "default"),
        },
        "instructions": instructions,
        "runtime": {"abi_version": 1, "image_digest": digest},
        "limits": {"max_turns": int(os.environ.get("MODEL_MAX_TURNS", "8")), "timeout_seconds": 300},
        "checkpoint": {"mode": "on_node_end", "require_checkpoint": False},
    }
    if tools:
        spec["tools"] = tools
    if orchestration:
        spec["orchestration"] = orchestration
    if mcp_servers:
        spec["mcp_servers"] = mcp_servers
    if output_schema:
        spec["output_schema"] = output_schema
    return spec


def deploy_agent(client: Client, name: str, instructions: str, **kwargs: object) -> Agent:
    agent = client.agents.create(name, agent_spec(instructions, **kwargs))  # type: ignore[arg-type]
    client.agents.deploy(agent.id, agent.latest_version)
    print(f"agent {name}: {agent.id} v{agent.latest_version} published")
    return agent


def run_workflow(client: Client, name: str, graph: dict, input_data: dict, *,
                 timeout: float = 300) -> Execution:
    workflow = client.workflows.create(name, graph)
    client.workflows.deploy(workflow.id, workflow.latest_version)
    execution = client.workflows.execute(workflow.id, workflow.latest_version, input_data)
    print(f"execution {execution.id} submitted")
    final = client.executions.wait(execution.id, timeout=timeout)
    print(f"execution {execution.id}: {final.state}")
    if final.failure_code:
        print(f"failure: {final.failure_code} {final.failure_detail}")
    return final


def agent_node(node_id: str, agent_id: str, *, version: int = 1,
               pool: str | None = None, input_from: list[str] | None = None) -> dict:
    pool = pool or os.environ.get("AGENT_PLATFORM_POOL", "default")
    node = {
        "id": node_id,
        "type": "agent",
        "agent": {"agent_id": agent_id, "version": version},
        "worker_pool": pool,
        "timeout_seconds": 300,
    }
    if input_from:
        node["input_from"] = input_from
    return node


def repo_root() -> str:
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def mcp_fixture_command() -> tuple[str, list[str]]:
    return sys.executable, ["-u", os.path.join(repo_root(), "runtimes", "python", "tests", "mcp_test_server.py")]
