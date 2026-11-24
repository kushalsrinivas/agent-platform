"""10. Error recovery: cancel a long delay, then resume it as a new execution.

`resume` is safe: it reuses only nodes whose outputs already succeeded and re-runs the rest.
"""

from __future__ import annotations

import time

from common import agent_node, api, deploy_agent


def main() -> None:
    with api() as client:
        agent = deploy_agent(client, "recovery-agent", "Report after recovery.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "wait", "type": "delay", "config": {"delay": {"seconds": 8}}},
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
        workflow = client.workflows.create("recovery-flow", graph)
        client.workflows.deploy(workflow.id, 1)
        execution = client.workflows.execute(workflow.id, 1, {"topic": "recover me"})
        time.sleep(1)
        client.executions.cancel(execution.id, reason="example cancellation")
        cancelled = client.executions.wait(execution.id, timeout=60)
        print("cancelled:", cancelled.state)

        resumed = client.executions.resume(execution.id)
        print("resumed:", resumed.id)
        final = client.executions.wait(resumed.id, timeout=180)
        print("final:", final.state)
        for event in client.executions.events(final.id):
            print(event.seq, event.event_type, event.reason or "")


if __name__ == "__main__":
    main()
