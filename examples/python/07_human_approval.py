"""7. Human approval before an agent runs."""

from __future__ import annotations

import time

from common import agent_node, api, deploy_agent


def main() -> None:
    with api() as client:
        agent = deploy_agent(client, "approved-agent", "Run only after a human approves.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "review", "type": "approval", "config": {"approval": {
                    "prompt": "Run the approved agent?",
                    "policy": "any",
                    "expires_in_seconds": 3600,
                    "reject_reason_required": True,
                }}},
                agent_node("work", agent.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "review"},
                {"from": "review", "to": "work"},
                {"from": "work", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        workflow = client.workflows.create("approval-flow", graph)
        client.workflows.deploy(workflow.id, 1)
        execution = client.workflows.execute(workflow.id, 1, {"topic": "approval demo"})
        print("execution", execution.id)
        for _ in range(60):
            approvals = client.executions.approvals(execution.id)
            if approvals and approvals[0].status == "pending":
                client.approvals.approve(execution.id, approvals[0].task_id, comment="approved by example")
                print("approved", approvals[0].id)
                break
            time.sleep(0.5)
        final = client.executions.wait(execution.id, timeout=120)
        print(execution.id, final.state)


if __name__ == "__main__":
    main()
