"""3. Multi-agent workflow: research, analysis and writing agents in sequence."""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        research = deploy_agent(client, "research-specialist", "Find relevant facts.")
        analysis = deploy_agent(client, "analysis-specialist", "Analyse the research.")
        writer = deploy_agent(client, "writing-specialist", "Write the final brief.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                agent_node("research", research.id),
                agent_node("analysis", analysis.id, input_from=["research"]),
                agent_node("write", writer.id, input_from=["analysis"]),
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "research"},
                {"from": "research", "to": "analysis"},
                {"from": "analysis", "to": "write"},
                {"from": "write", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        final = run_workflow(client, "multi-agent-flow", graph, {"topic": "durable AI workflows"})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
