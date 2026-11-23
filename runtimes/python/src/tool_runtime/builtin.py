"""Built-in tools available to every workflow.

They are deterministic and side-effect free, which makes them safe under at-least-once delivery.
"""

from __future__ import annotations

import asyncio
import os
from pathlib import Path
from typing import Any


def _record_tool_call(path: str, idempotency_key: str) -> None:
    with Path(path).open("a", encoding="utf-8") as handle:
        handle.write(f"{idempotency_key}\n")


class EchoTool:
    name = "echo"
    version = "1"

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        del idempotency_key
        return {"echo": arguments}


class WebSearchTool:
    """Deterministic stand-in for the platform web-search tool.

    Phase 1 has no network egress from the sandbox; the control-plane workflow references the tool by name and
    version, and the runtime returns a stable, inspectable result.
    """

    name = "tool_web_search"
    version = "1"

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        del idempotency_key
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

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        counter = os.environ.get("AGENT_RUNTIME_TOOL_COUNTER_FILE", "").strip()
        if counter:
            await asyncio.to_thread(_record_tool_call, counter, idempotency_key)
        numbers = arguments.get("numbers")
        if not isinstance(numbers, list) or not numbers:
            raise ValueError("numbers must be a non-empty list")
        if not all(isinstance(n, int | float) and not isinstance(n, bool) for n in numbers):
            raise ValueError("numbers must contain only numbers")
        return {"sum": sum(numbers)}
