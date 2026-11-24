"""1. Simple LLM agent.

Run: python examples/python/01_simple_llm_agent.py
Environment: AGENT_PLATFORM_API_KEY, AGENT_PLATFORM_ORG, AGENT_PLATFORM_PROJECT.
"""

from __future__ import annotations

from common import agent_node, api, deploy_agent, run_workflow


def main() -> None:
    with api() as client:
        agent = deploy_agent(client, "simple-agent", "Answer the user's topic in one paragraph.")
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                agent_node("answer", agent.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [{"from": "start", "to": "answer"}, {"from": "answer", "to": "end"}],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        final = run_workflow(client, "simple-agent-flow", graph, {"topic": "durable execution"})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
