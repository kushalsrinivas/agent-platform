"""MCP client protocol and descriptors."""

from __future__ import annotations

from typing import Any, Literal, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field

MCPTransport = Literal["stdio", "sse", "streamable_http"]


class MCPServerConfig(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str
    transport: MCPTransport
    command: str | None = None
    args: tuple[str, ...] = ()
    url: str | None = None
    timeout_seconds: float = Field(default=30.0, gt=0, le=300)


class MCPTool(BaseModel):
    model_config = ConfigDict(frozen=True)

    server: str
    name: str
    description: str = ""
    input_schema: dict[str, Any] = Field(default_factory=dict)


@runtime_checkable
class MCPClient(Protocol):
    async def list_tools(self) -> list[MCPTool]: ...

    async def call_tool(self, server: str, name: str, arguments: dict[str, Any]) -> dict[str, Any]: ...
