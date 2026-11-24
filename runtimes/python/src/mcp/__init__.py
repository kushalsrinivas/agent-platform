"""MCP integration surface.

Descriptors are immutable so a checkpoint can record which server and tool version produced a result. The manager
and transports live in :mod:`mcp.manager`.
"""

from mcp.client import MCPClient, MCPServerConfig, MCPTool, MCPTransport

__all__ = ["MCPClient", "MCPServerConfig", "MCPTool", "MCPTransport"]
