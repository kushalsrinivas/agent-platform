"""Tool registry and execution boundary."""

from __future__ import annotations

import logging
from typing import Any, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field

log = logging.getLogger(__name__)


class ToolResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    ok: bool
    output: dict[str, Any] = Field(default_factory=dict)
    error_code: str | None = None
    error_message: str | None = None


@runtime_checkable
class Tool(Protocol):
    name: str
    version: str

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]: ...


class ToolRegistry:
    """Name -> implementation. Versions are exposed for the workflow pin; one version per name in an image."""

    def __init__(self) -> None:
        self._tools: dict[str, Tool] = {}

    def register(self, tool: Tool) -> None:
        if tool.name in self._tools:
            raise ValueError(f"tool {tool.name!r} already registered")
        self._tools[tool.name] = tool

    def get(self, name: str) -> Tool | None:
        return self._tools.get(name)

    def versions(self) -> dict[str, str]:
        return {name: tool.version for name, tool in self._tools.items()}

    async def execute(self, name: str, arguments: dict[str, Any], *, idempotency_key: str = "") -> ToolResult:
        tool = self._tools.get(name)
        if tool is None:
            return ToolResult(
                ok=False,
                error_code="unknown_tool",
                error_message=f"tool {name!r} is not available in this runtime image",
            )
        try:
            output = await tool.run(arguments, idempotency_key=idempotency_key)
        except (TypeError, ValueError) as exc:
            log.warning("tool rejected input", extra={"tool": name, "error": str(exc)})
            return ToolResult(ok=False, error_code="invalid_arguments", error_message=str(exc))
        except Exception:
            log.exception("tool failed", extra={"tool": name})
            return ToolResult(ok=False, error_code="tool_failed", error_message="tool execution failed")
        return ToolResult(ok=True, output=output)
