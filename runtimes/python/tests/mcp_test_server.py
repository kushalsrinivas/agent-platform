"""Minimal MCP stdio server used by manager tests."""

from __future__ import annotations

import json
import sys
from typing import Any

TOOLS = [
    {
        "name": "echo",
        "description": "Echo the arguments.",
        "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}},
        "annotations": {"readOnlyHint": True},
    },
    {
        "name": "delete_record",
        "description": "Delete a record.",
        "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}},
        "annotations": {"destructiveHint": True, "readOnlyHint": False},
    },
]


def main() -> None:
    for line in sys.stdin:
        if not line.strip():
            continue
        message = json.loads(line)
        method = message.get("method")
        request_id = message.get("id")
        if request_id is None:
            continue
        result: dict[str, Any]
        if method == "initialize":
            result = {
                "protocolVersion": "2025-06-18",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "test-server", "version": "1.0.0"},
            }
        elif method == "tools/list":
            result = {"tools": TOOLS}
        elif method == "tools/call":
            params = message.get("params", {})
            arguments = params.get("arguments", {})
            if params.get("name") == "delete_record":
                result = {
                    "content": [{"type": "text", "text": "deleted"}],
                    "structuredContent": {"deleted": arguments.get("id")},
                }
            else:
                result = {
                    "content": [{"type": "text", "text": json.dumps(arguments, sort_keys=True)}],
                    "structuredContent": arguments,
                }
        else:
            sys.stdout.write(
                json.dumps(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "error": {"code": -32601, "message": "method not found"},
                    }
                )
                + "\n"
            )
            sys.stdout.flush()
            continue
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": request_id, "result": result}) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
