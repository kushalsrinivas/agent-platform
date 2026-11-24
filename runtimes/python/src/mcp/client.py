"""MCP client protocol, descriptors and server registration models."""

from __future__ import annotations

from typing import Any, Literal, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field

from tool_runtime.policy import ToolPolicy

MCPTransport = Literal["stdio", "streamable_http"]


class MCPServerConfig(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str = Field(min_length=1, max_length=64)
    transport: MCPTransport
    command: str | None = None
    args: tuple[str, ...] = ()
    env: dict[str, str] = Field(default_factory=dict)
    url: str | None = None
    timeout_seconds: float = Field(default=30.0, gt=0, le=300)
    auth_credential_ref: str | None = Field(default=None, max_length=120)
    tool_policies: dict[str, ToolPolicy] = Field(default_factory=dict)


class MCPTool(BaseModel):
    model_config = ConfigDict(frozen=True)

    server: str
    name: str
    description: str = ""
    input_schema: dict[str, Any] = Field(default_factory=dict)
    annotations: dict[str, Any] = Field(default_factory=dict)


@runtime_checkable
class MCPClient(Protocol):
    async def list_tools(self) -> list[MCPTool]: ...

    async def call_tool(self, server: str, name: str, arguments: dict[str, Any]) -> dict[str, Any]: ...
