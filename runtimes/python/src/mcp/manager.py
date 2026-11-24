"""MCP manager: registry, transports, discovery, invocation and tracing.

Two transports are supported: newline-delimited JSON-RPC over a child process (``stdio``) and Streamable HTTP
JSON-RPC. Connections are lazy, serialised per server with a lock, and always closed by the owning runtime
process. The manager never persists credentials and never logs authorization headers.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import logging
import os
import re
import time
from collections.abc import Iterable
from typing import Any, Protocol

import httpx

from mcp.client import MCPServerConfig, MCPTool
from mcp.network_policy import URLPolicyError, validate_outbound_url
from tool_runtime.policy import MCP_DEFAULT_POLICY, ToolPolicy

log = logging.getLogger(__name__)

PROTOCOL_VERSION = "2025-06-18"
MAX_TEXT_BYTES = 1 << 20
MAX_RESULT_BYTES = 4 << 20

_SECRET_PATTERN = re.compile(r"(?i)(apk_[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{12,}|sk-[A-Za-z0-9_-]{12,})")


def _redact(value: str) -> str:
    return _SECRET_PATTERN.sub("[redacted]", value)


def _stdio_allowed() -> bool:
    return os.getenv("AGENT_MCP_ALLOW_STDIO", "false").strip().lower() in {"1", "true", "yes"}


class MCPError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def _normalise_content(result: dict[str, Any]) -> dict[str, Any]:
    content: list[dict[str, Any]] = []
    total = 0
    for block in result.get("content", []) or []:
        if not isinstance(block, dict):
            continue
        kind = block.get("type")
        normalised: dict[str, Any] = {"type": kind}
        if kind == "text":
            text = str(block.get("text", ""))
            encoded = text.encode("utf-8")
            if len(encoded) > MAX_TEXT_BYTES:
                text = encoded[:MAX_TEXT_BYTES].decode("utf-8", "ignore") + "\n[truncated]"
            normalised["text"] = text
            total += len(text.encode("utf-8"))
        elif kind == "image":
            # Content is not inlined into execution state; keep only the descriptor.
            normalised["mime_type"] = block.get("mimeType", "")
            normalised["data_bytes"] = len(str(block.get("data", "")))
        elif kind == "resource":
            normalised["resource"] = block.get("resource", {})
        else:
            normalised["raw"] = block
        content.append(normalised)
        if total > MAX_RESULT_BYTES:
            content.append({"type": "text", "text": "[result truncated]"})
            break
    return {
        "content": content,
        "structured": result.get("structuredContent"),
        "is_error": bool(result.get("isError", False)),
    }


class _Connection(Protocol):
    name: str

    async def start(self) -> None: ...

    async def list_tools(self) -> list[MCPTool]: ...

    async def call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]: ...

    async def close(self) -> None: ...


class _JSONRPC:
    """Shared request bookkeeping for both transports."""

    def __init__(self, name: str) -> None:
        self.name = name
        self._next_id = 0
        self._lock = asyncio.Lock()
        self._closed = False

    def request_id(self) -> int:
        self._next_id += 1
        return self._next_id

    @property
    def closed(self) -> bool:
        return self._closed


class StdioConnection(_JSONRPC):
    def __init__(self, config: MCPServerConfig, token: str | None) -> None:
        super().__init__(config.name)
        self._config = config
        self._token = token
        self._process: asyncio.subprocess.Process | None = None
        self._stderr_task: asyncio.Task[None] | None = None

    async def start(self) -> None:
        if not _stdio_allowed():
            raise MCPError("MCP_STDIO_DISABLED", f"stdio server {self.name!r} is disabled by runtime policy")
        if not self._config.command:
            raise MCPError("MCP_CONFIG_INVALID", f"stdio server {self.name!r} has no command")
        environment = dict(os.environ)
        environment.update(self._config.env)
        if self._token:
            environment.setdefault("MCP_AUTH_TOKEN", self._token)
        self._process = await asyncio.create_subprocess_exec(
            self._config.command,
            *self._config.args,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=environment,
        )
        assert self._process.stderr is not None
        self._stderr_task = asyncio.create_task(self._pump_stderr(self._process.stderr))
        await self._initialize()

    async def _pump_stderr(self, stream: asyncio.StreamReader) -> None:
        while True:
            line = await stream.readline()
            if not line:
                return
            text = _redact(line.decode("utf-8", "replace")[:512])
            log.debug("mcp_server_stderr", extra={"server": self.name, "line": text})

    async def _send(self, payload: dict[str, Any]) -> None:
        assert self._process is not None and self._process.stdin is not None
        self._process.stdin.write((json.dumps(payload, separators=(",", ":")) + "\n").encode("utf-8"))
        await self._process.stdin.drain()

    async def _request(self, method: str, params: dict[str, Any] | None = None) -> Any:
        async with self._lock:
            assert self._process is not None and self._process.stdout is not None
            request_id = self.request_id()
            await self._send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}})
            deadline = time.monotonic() + self._config.timeout_seconds
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise MCPError("MCP_TIMEOUT", f"server {self.name!r} timed out")
                try:
                    line = await asyncio.wait_for(self._process.stdout.readline(), timeout=remaining)
                except TimeoutError as exc:
                    raise MCPError("MCP_TIMEOUT", f"server {self.name!r} timed out") from exc
                if not line:
                    raise MCPError("MCP_CONNECTION_CLOSED", f"server {self.name!r} closed the connection")
                try:
                    message = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if message.get("id") != request_id:
                    if "method" in message and "id" in message:
                        await self._send(
                            {
                                "jsonrpc": "2.0",
                                "id": message["id"],
                                "error": {"code": -32601, "message": "method not supported by client"},
                            }
                        )
                    continue
                if "error" in message:
                    error = message["error"] or {}
                    raise MCPError(f"MCP_ERROR_{error.get('code', 'UNKNOWN')}", str(error.get("message", "MCP error")))
                return message.get("result")

    async def _notify(self, method: str, params: dict[str, Any] | None = None) -> None:
        async with self._lock:
            await self._send({"jsonrpc": "2.0", "method": method, "params": params or {}})

    async def _initialize(self) -> None:
        result = await self._request(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "agent-platform", "version": "1.0.0"},
            },
        )
        negotiated = result.get("protocolVersion") if isinstance(result, dict) else None
        if negotiated and negotiated != PROTOCOL_VERSION:
            log.info("mcp_protocol_negotiated", extra={"server": self.name, "protocol": negotiated})
        await self._notify("notifications/initialized")

    async def list_tools(self) -> list[MCPTool]:
        result = await self._request("tools/list")
        tools: list[MCPTool] = []
        for item in (result or {}).get("tools", []):
            if not isinstance(item, dict) or not item.get("name"):
                continue
            tools.append(
                MCPTool(
                    server=self.name,
                    name=str(item["name"]),
                    description=str(item.get("description", "")),
                    input_schema=dict(item.get("inputSchema") or {}),
                    annotations=dict(item.get("annotations") or {}),
                )
            )
        return tools

    async def call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("tools/call", {"name": name, "arguments": arguments})
        return _normalise_content(result if isinstance(result, dict) else {})

    async def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        if self._process is not None:
            self._process.terminate()
            try:
                await asyncio.wait_for(self._process.wait(), timeout=3)
            except TimeoutError:
                self._process.kill()
                await self._process.wait()
        if self._stderr_task is not None:
            self._stderr_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._stderr_task


class HTTPConnection(_JSONRPC):
    def __init__(self, config: MCPServerConfig, token: str | None) -> None:
        super().__init__(config.name)
        self._config = config
        self._token = token
        self._client = httpx.AsyncClient(timeout=config.timeout_seconds, follow_redirects=False)
        self._session_id: str | None = None

    def _headers(self) -> dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": PROTOCOL_VERSION,
        }
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        if self._session_id:
            headers["Mcp-Session-Id"] = self._session_id
        return headers

    async def _post(self, payload: dict[str, Any]) -> httpx.Response:
        assert self._config.url
        try:
            validate_outbound_url(self._config.url)
        except URLPolicyError as exc:
            raise MCPError("MCP_URL_BLOCKED", str(exc)) from exc
        try:
            response = await self._client.post(self._config.url, json=payload, headers=self._headers())
        except httpx.TimeoutException as exc:
            raise MCPError("MCP_TIMEOUT", f"server {self.name!r} timed out") from exc
        except httpx.HTTPError as exc:
            raise MCPError("MCP_CONNECTION_FAILED", f"server {self.name!r} is unreachable") from exc
        session = response.headers.get("mcp-session-id")
        if session:
            self._session_id = session
        if response.status_code >= 400:
            raise MCPError(
                f"MCP_HTTP_{response.status_code}",
                f"server {self.name!r} returned HTTP {response.status_code}",
            )
        return response

    def _parse(self, response: httpx.Response, request_id: int) -> dict[str, Any]:
        if not response.content:
            return {}
        content_type = response.headers.get("content-type", "")
        if "text/event-stream" in content_type:
            fallback: dict[str, Any] | None = None
            for line in response.text.splitlines():
                if not line.startswith("data:"):
                    continue
                payload = line[5:].strip()
                if not payload:
                    continue
                with contextlib.suppress(json.JSONDecodeError):
                    value = json.loads(payload)
                    if not isinstance(value, dict):
                        continue
                    if value.get("id") == request_id:
                        return value
                    if "id" not in value:
                        fallback = value
            return fallback or {}
        try:
            value = response.json()
        except json.JSONDecodeError as exc:
            raise MCPError("MCP_INVALID_RESPONSE", f"server {self.name!r} returned invalid JSON") from exc
        if isinstance(value, dict) and value.get("id") not in (None, request_id):
            raise MCPError("MCP_INVALID_RESPONSE", f"server {self.name!r} returned a mismatched response id")
        return value if isinstance(value, dict) else {}

    async def _request(self, method: str, params: dict[str, Any] | None = None) -> Any:
        async with self._lock:
            request_id = self.request_id()
            response = await self._post({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}})
            message = self._parse(response, request_id)
            if not message:
                # Retrying a request could duplicate a side effect; fail closed instead.
                raise MCPError("MCP_INVALID_RESPONSE", f"server {self.name!r} returned an empty response")
            if "error" in message:
                error = message.get("error") or {}
                raise MCPError(f"MCP_ERROR_{error.get('code', 'UNKNOWN')}", str(error.get("message", "MCP error")))
            return message.get("result")

    async def _initialize(self) -> None:
        result = await self._request(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "agent-platform", "version": "1.0.0"},
            },
        )
        negotiated = result.get("protocolVersion") if isinstance(result, dict) else None
        if negotiated and negotiated != PROTOCOL_VERSION:
            log.info("mcp_protocol_negotiated", extra={"server": self.name, "protocol": negotiated})
        await self._post({"jsonrpc": "2.0", "method": "notifications/initialized"})

    async def start(self) -> None:
        assert self._config.url
        try:
            validate_outbound_url(self._config.url)
        except URLPolicyError as exc:
            raise MCPError("MCP_URL_BLOCKED", str(exc)) from exc
        await self._initialize()

    async def list_tools(self) -> list[MCPTool]:
        result = await self._request("tools/list")
        tools: list[MCPTool] = []
        for item in (result or {}).get("tools", []):
            if not isinstance(item, dict) or not item.get("name"):
                continue
            tools.append(
                MCPTool(
                    server=self.name,
                    name=str(item["name"]),
                    description=str(item.get("description", "")),
                    input_schema=dict(item.get("inputSchema") or {}),
                    annotations=dict(item.get("annotations") or {}),
                )
            )
        return tools

    async def call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("tools/call", {"name": name, "arguments": arguments})
        return _normalise_content(result if isinstance(result, dict) else {})

    async def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        if self._session_id and self._config.url:
            with contextlib.suppress(httpx.HTTPError):
                await self._client.delete(self._config.url, headers=self._headers())
        await self._client.aclose()


class MCPManager:
    """Owns every configured connection for one runtime attempt."""

    def __init__(self, servers: Iterable[MCPServerConfig], credentials: dict[str, str] | None = None) -> None:
        self._configs = {server.name: server for server in servers}
        self._credentials = credentials or {}
        self._connections: dict[str, _Connection] = {}
        self._tools: list[MCPTool] = []
        self._started = False
        self._trace: list[dict[str, Any]] = []

    def credential_available(self, server: str) -> bool:
        config = self._configs.get(server)
        if config is None or not config.auth_credential_ref:
            return True
        return self._token(config) is not None

    def _token(self, config: MCPServerConfig) -> str | None:
        if not config.auth_credential_ref:
            return None
        token = self._credentials.get(config.auth_credential_ref)
        if token:
            return token
        # Development fallback: a secret store integration supplies credentials through the context. An env
        # variable keeps local MCP servers usable without inlining secrets in the immutable agent spec.
        return os.environ.get(f"MCP_CREDENTIAL_{config.auth_credential_ref.upper().replace('-', '_')}")

    async def start(self) -> None:
        if self._started:
            return
        self._started = True
        for name, config in self._configs.items():
            token = self._token(config)
            connection: _Connection
            if config.transport == "stdio":
                connection = StdioConnection(config, token)
            else:
                connection = HTTPConnection(config, token)
            try:
                await connection.start()
                self._connections[name] = connection
                self._tools.extend(await connection.list_tools())
            except MCPError:
                await connection.close()
                raise
            except Exception as exc:  # pragma: no cover - defensive boundary
                await connection.close()
                raise MCPError("MCP_START_FAILED", f"server {name!r} failed to start") from exc

    @property
    def tools(self) -> list[MCPTool]:
        return list(self._tools)

    @property
    def trace(self) -> list[dict[str, Any]]:
        return list(self._trace)

    def policy(self, tool: MCPTool) -> ToolPolicy:
        config = self._configs.get(tool.server)
        if config is None:
            return MCP_DEFAULT_POLICY
        return config.tool_policies.get(tool.name, MCP_DEFAULT_POLICY)

    async def call_tool(self, server: str, tool: str, arguments: dict[str, Any]) -> dict[str, Any]:
        connection = self._connections.get(server)
        if connection is None:
            raise MCPError("MCP_SERVER_UNKNOWN", f"server {server!r} is not connected")
        started = time.monotonic()
        try:
            result = await connection.call_tool(tool, arguments)
        except MCPError as exc:
            self._trace.append(
                {
                    "server": server,
                    "tool": tool,
                    "ok": False,
                    "error_code": exc.code,
                    "duration_ms": int((time.monotonic() - started) * 1000),
                }
            )
            raise
        self._trace.append(
            {
                "server": server,
                "tool": tool,
                "ok": not result.get("is_error", False),
                "duration_ms": int((time.monotonic() - started) * 1000),
            }
        )
        log.info(
            "mcp_tool_call",
            extra={
                "server": server,
                "tool": tool,
                "ok": not result.get("is_error", False),
                "duration_ms": int((time.monotonic() - started) * 1000),
            },
        )
        return result

    async def close(self) -> None:
        for connection in self._connections.values():
            with contextlib.suppress(Exception):
                await connection.close()
        self._connections.clear()
        self._tools.clear()
        self._started = False

    async def __aenter__(self) -> MCPManager:
        await self.start()
        return self

    async def __aexit__(self, *_: Any) -> None:
        await self.close()
