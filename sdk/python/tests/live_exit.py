"""Live exit-criterion script: install-path SDK with an API key, no internal clients."""

from __future__ import annotations

import json
import os
import sys

from platform_sdk import Client

SPEC = {
    "model": {"provider": "echo", "name": "echo-1", "credential_ref": "default"},
    "instructions": "Return the input.",
    "runtime": {"abi_version": 1, "image_digest": "sha256:" + "a" * 64},
    "limits": {"max_turns": 4, "timeout_seconds": 60},
    "checkpoint": {"mode": "on_node_end", "require_checkpoint": False},
}


def main() -> int:
    with Client(
        base_url=os.environ["AGENT_PLATFORM_URL"],
        api_key=os.environ["AGENT_PLATFORM_API_KEY"],
        org_id=os.environ["AGENT_PLATFORM_ORG"],
        project_id=os.environ["AGENT_PLATFORM_PROJECT"],
        timeout=60,
    ) as client:
        agent = client.agents.create("python-sdk-agent", SPEC)
        client.agents.deploy(agent.id, 1)
        workflow = client.workflows.create(
            "python-sdk-flow",
            {
                "nodes": [
                    {"id": "start", "type": "start"},
                    {
                        "id": "answer",
                        "type": "agent",
                        "agent": {"agent_id": agent.id, "version": 1},
                        "worker_pool": os.environ.get("AGENT_PLATFORM_POOL", "default"),
                        "timeout_seconds": 60,
                    },
                    {"id": "end", "type": "end"},
                ],
                "edges": [{"from": "start", "to": "answer"}, {"from": "answer", "to": "end"}],
                "input_schema": {"type": "object", "required": ["topic"]},
            },
        )
        client.workflows.deploy(workflow.id, 1)
        execution = client.workflows.execute(workflow.id, 1, {"topic": "python sdk"})
        events = list(client.executions.stream_events(execution.id))
        final = client.executions.wait(execution.id, timeout=60)
        print(
            json.dumps(
                {
                    "execution_id": final.id,
                    "state": final.state,
                    "events": len(events),
                    "tools": len(client.tools.list().items),
                },
                sort_keys=True,
            )
        )
        return 0 if final.state == "succeeded" and events else 1


if __name__ == "__main__":
    sys.exit(main())
