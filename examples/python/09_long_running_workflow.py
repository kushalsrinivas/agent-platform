"""9. Long-running workflow with a durable delay.

The delay is persisted in the orchestrator; no worker sleeps or holds a slot while waiting.
"""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        agent = deploy_agent(client, "long-running-agent", "Report after the wait.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "wait", "type": "delay", "config": {"delay": {"seconds": 5}}},
                agent_node("report", agent.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "wait"},
                {"from": "wait", "to": "report"},
                {"from": "report", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        final = run_workflow(client, "long-running-flow", graph, {"topic": "long running"}, timeout=180)
        print(final.id, final.state)


if __name__ == "__main__":
    main()
