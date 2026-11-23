"""Built-in tools available to every workflow.

They are deterministic and side-effect free, which makes them safe under at-least-once delivery.
"""

from __future__ import annotations

from typing import Any


class EchoTool:
    name = "echo"
    version = "1"

    async def run(self, arguments: dict[str, Any]) -> dict[str, Any]:
        return {"echo": arguments}


class WebSearchTool:
    """Deterministic stand-in for the platform web-search tool.

    Phase 1 has no network egress from the sandbox; the control-plane workflow references the tool by name and
    version, and the runtime returns a stable, inspectable result.
    """

    name = "tool_web_search"
    version = "1"

    async def run(self, arguments: dict[str, Any]) -> dict[str, Any]:
        query = arguments.get("query") or arguments.get("input") or ""
        return {
            "query": str(query),
            "results": [
                {"title": f"Result for {query}", "url": "https://example.invalid/result"},
            ],
        }


class AddTool:
    name = "add"
    version = "1"

    async def run(self, arguments: dict[str, Any]) -> dict[str, Any]:
        numbers = arguments.get("numbers")
        if not isinstance(numbers, list) or not numbers:
            raise ValueError("numbers must be a non-empty list")
        if not all(isinstance(n, int | float) and not isinstance(n, bool) for n in numbers):
            raise ValueError("numbers must contain only numbers")
        return {"sum": sum(numbers)}
