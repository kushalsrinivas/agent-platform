"""4. Parallel execution with a join before the final response."""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        web = deploy_agent(client, "web-specialist", "Summarise the web branch.")
        data = deploy_agent(client, "data-specialist", "Summarise the data branch.")
        writer = deploy_agent(client, "parallel-writer", "Combine the branch summaries.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "fan", "type": "parallel"},
                agent_node("web", web.id),
                agent_node("data", data.id),
                {"id": "join", "type": "join", "config": {"join": {"mode": "all"}}},
                agent_node("write", writer.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "fan"},
                {"from": "fan", "to": "web", "priority": 1},
                {"from": "fan", "to": "data", "priority": 2},
                {"from": "web", "to": "join"},
                {"from": "data", "to": "join"},
                {"from": "join", "to": "write"},
                {"from": "write", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        final = run_workflow(client, "parallel-flow", graph, {"topic": "parallel research"})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
