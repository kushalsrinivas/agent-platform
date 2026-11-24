"""6. MCP-enabled agent using a local stdio MCP server.

The fixture server is in runtimes/python/tests/mcp_test_server.py. In production, point
`command`/`args` or `url` at your own MCP server and use auth_credential_ref for secrets.
"""

from __future__ import annotations

from common import agent_node, api, deploy_agent, mcp_fixture_command, run_workflow


def main() -> None:
    command, args = mcp_fixture_command()
    with api() as client:
        agent = deploy_agent(
            client,
            "mcp-agent",
            "Use the fixture MCP tools when asked.",
            mcp_servers=[{
                "name": "fixture",
                "transport": "stdio",
                "command": command,
                "args": args,
                "timeout_seconds": 15,
                "tool_policies": {"echo": {"permissions": {"read": True, "network": True}}},
            }],
        )
        graph = {
            "nodes": [
                {"id": "start", "type": "start"},
                agent_node("mcp", agent.id),
                {"id": "end", "type": "end"},
            ],
            "edges": [{"from": "start", "to": "mcp"}, {"from": "mcp", "to": "end"}],
            "input_schema": {"type": "object", "required": ["topic"]},
        }
        topic = 'tool:mcp:{"server":"fixture","tool":"echo","arguments":{"text":"hello from MCP"}}'
        final = run_workflow(client, "mcp-flow", graph, {"topic": topic})
        print(final.id, final.state)


if __name__ == "__main__":
    main()
