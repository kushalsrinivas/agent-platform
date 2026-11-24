"""8. Scheduled workflow.

Run once:  python examples/python/08_scheduled_workflow.py --once
Run always: python examples/python/08_scheduled_workflow.py
In production, run this process under cron, systemd or your scheduler. The idempotency key is
the schedule slot, so a retried tick cannot create a duplicate execution.
"""

from __future__ import annotations

import argparse
import time
from datetime import UTC, datetime

from common import agent_node, api, deploy_agent


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--interval", type=float, default=60.0)
    args = parser.parse_args()
    with api() as client:
        agent = deploy_agent(client, "scheduled-agent", "Produce the scheduled report.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                agent_node("report", agent.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [{"from": "start", "to": "report"}, {"from": "report", "to": "end"}],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        workflow = client.workflows.create("scheduled-flow", graph)
        client.workflows.deploy(workflow.id, 1)
        while True:
            slot = datetime.now(UTC).strftime("%Y-%m-%dT%H:%M")
            execution = client.workflows.execute(
                workflow.id, 1, {"topic": f"scheduled {slot}"},
                idempotency_key=f"schedule-{workflow.id}-{slot}",
            )
            final = client.executions.wait(execution.id, timeout=300)
            print(slot, execution.id, final.state)
            if args.once:
                return
            time.sleep(args.interval)


if __name__ == "__main__":
    main()
