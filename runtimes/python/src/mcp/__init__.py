"""MCP integration surface.

Phase 02 defines the descriptors and the client protocol the runtime will speak; the stdio/SSE transports and
tool discovery land with MCP support. Descriptors are immutable so a checkpoint can record which server and tool
version produced a result.
"""

from mcp.client import MCPClient, MCPServerConfig, MCPTool, MCPTransport

__all__ = ["MCPClient", "MCPServerConfig", "MCPTool", "MCPTransport"]
