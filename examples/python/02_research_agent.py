"""2. Research agent with the web-search tool and an explicit data edge."""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        agent = deploy_agent(
            client,
            "research-agent",
            "Synthesise the supplied search results into a short research brief.",
            tools=[{"tool_id": "tool_web_search", "version": 1}],
        )
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                {"id": "search", "type": "tool", "worker_pool": "default", "timeout_seconds": 60,
                 "tool": {"tool_id": "tool_web_search", "version": 1}},
                {**agent_node("research", agent.id, input_from=["search"]),
                 "params": {"instructions": "Summarise the search results."}},
                {"id": "end", "type": "end"},
            ],
            "edges": [
                {"from": "start", "to": "search"},
                {"from": "search", "to": "research"},
                {"from": "research", "to": "end"},
            ],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        final = run_workflow(client, "research-flow", graph, {"topic": "agent platforms"})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
