"""MCP manager transport, discovery and normalisation tests."""

from __future__ import annotations

import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

from mcp.client import MCPServerConfig
from mcp.manager import MCPError, MCPManager


@pytest.fixture(autouse=True)
def _allow_local_transports(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("AGENT_MCP_ALLOW_STDIO", "true")
    monkeypatch.setenv("AGENT_MCP_ALLOW_PRIVATE_NETWORKS", "true")


def _server_config() -> MCPServerConfig:
    return MCPServerConfig(
        name="fixture",
        transport="stdio",
        command=sys.executable,
        args=("-u", str(Path(__file__).with_name("mcp_test_server.py"))),
        timeout_seconds=10,
    )


@pytest.mark.asyncio
async def test_stdio_discovery_and_call() -> None:
    manager = MCPManager([_server_config()])
    await manager.start()
    try:
        tools = {tool.name: tool for tool in manager.tools}
        assert set(tools) == {"echo", "delete_record"}
        assert tools["echo"].input_schema["type"] == "object"
        assert tools["delete_record"].annotations["destructiveHint"] is True

        result = await manager.call_tool("fixture", "echo", {"text": "hello"})
        assert result["structured"] == {"text": "hello"}
        assert result["content"][0]["text"] == '{"text": "hello"}'
        assert manager.trace[-1]["ok"] is True
    finally:
        await manager.close()


@pytest.mark.asyncio
async def test_unknown_server_is_rejected() -> None:
    manager = MCPManager([_server_config()])
    await manager.start()
    try:
        with pytest.raises(MCPError) as caught:
            await manager.call_tool("missing", "echo", {})
        assert caught.value.code == "MCP_SERVER_UNKNOWN"
    finally:
        await manager.close()


def test_tool_policy_is_registered_per_server() -> None:
    config = _server_config()
    manager = MCPManager([config])
    from mcp.client import MCPTool

    tool = MCPTool(server="fixture", name="echo")
    assert manager.policy(tool).permissions.network is True


class _HTTPHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self) -> None:
        length = int(self.headers.get("content-length", "0"))
        message = json.loads(self.rfile.read(length) or b"{}")
        if message.get("id") is None:
            self.send_response(202)
            self.send_header("content-length", "0")
            self.end_headers()
            return
        method = message.get("method")
        if method == "initialize":
            result: dict[str, Any] = {
                "protocolVersion": "2025-06-18",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "http-fixture", "version": "1.0.0"},
            }
        elif method == "tools/list":
            result = {
                "tools": [
                    {
                        "name": "echo",
                        "description": "Echo",
                        "inputSchema": {"type": "object"},
                        "annotations": {"readOnlyHint": True},
                    }
                ]
            }
        elif method == "tools/call":
            arguments = message.get("params", {}).get("arguments", {})
            result = {"content": [{"type": "text", "text": "http-ok"}], "structuredContent": arguments}
        else:
            result = {}
        payload = json.dumps({"jsonrpc": "2.0", "id": message["id"], "result": result}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(payload)))
        self.send_header("mcp-session-id", "session-1")
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_args: Any) -> None:
        return


@pytest.mark.asyncio
async def test_streamable_http_discovery_and_call() -> None:
    server = ThreadingHTTPServer(("127.0.0.1", 0), _HTTPHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        config = MCPServerConfig(
            name="http-fixture",
            transport="streamable_http",
            url=f"http://127.0.0.1:{server.server_port}/mcp",
            timeout_seconds=10,
        )
        manager = MCPManager([config])
        await manager.start()
        try:
            assert [tool.name for tool in manager.tools] == ["echo"]
            result = await manager.call_tool("http-fixture", "echo", {"text": "hello"})
            assert result["structured"] == {"text": "hello"}
            assert result["content"][0]["text"] == "http-ok"
        finally:
            await manager.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@pytest.mark.asyncio
async def test_private_http_is_blocked_by_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("AGENT_MCP_ALLOW_PRIVATE_NETWORKS", raising=False)
    config = MCPServerConfig(
        name="blocked",
        transport="streamable_http",
        url="http://127.0.0.1:9/mcp",
        timeout_seconds=2,
    )
    manager = MCPManager([config])
    with pytest.raises(MCPError) as caught:
        await manager.start()
    assert caught.value.code == "MCP_URL_BLOCKED"


@pytest.mark.asyncio
async def test_stdio_is_blocked_by_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("AGENT_MCP_ALLOW_STDIO", raising=False)
    manager = MCPManager([_server_config()])
    with pytest.raises(MCPError) as caught:
        await manager.start()
    assert caught.value.code == "MCP_STDIO_DISABLED"
