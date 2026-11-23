"""Versioned, platform-provided tools.

Phase 1 has no tenant-authored tools: every tool ships with the runtime image and is referenced by name and
version from a workflow. Tool code runs in the same sandbox as the agent.
"""

from tool_runtime.builtin import AddTool, EchoTool, WebSearchTool
from tool_runtime.registry import Tool, ToolRegistry, ToolResult


def default_registry() -> ToolRegistry:
    """Registry with the Phase 1 built-ins."""
    registry = ToolRegistry()
    registry.register(EchoTool())
    registry.register(AddTool())
    registry.register(WebSearchTool())
    return registry


__all__ = [
    "AddTool",
    "EchoTool",
    "Tool",
    "ToolRegistry",
    "ToolResult",
    "WebSearchTool",
    "default_registry",
]
