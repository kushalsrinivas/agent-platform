"""5. Conditional workflow routed by a CEL expression."""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        premium = deploy_agent(client, "premium-agent", "Give the premium response.")
        standard = deploy_agent(client, "standard-agent", "Give the standard response.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "score", "type": "condition",
                 "config": {"expression": "input.score >= 80"}},
                agent_node("premium", premium.id),
                agent_node("standard", standard.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "score"},
                {"from": "score", "to": "premium", "when": "outputs.score.result == true"},
                {"from": "score", "to": "standard", "when": "outputs.score.result == false"},
                {"from": "premium", "to": "end"},
                {"from": "standard", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic", "score"]},
        }
        final = run_workflow(client, "conditional-flow", graph, {"topic": "routing", "score": 91})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
