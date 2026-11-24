"""Tool execution policy: permissions, timeout, concurrency, retries and idempotency.

The policy is declared in the immutable agent version and enforced at the runtime boundary. It is deliberately
conservative: writes, destructive operations and network access are denied unless the policy opts in.
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

from tool_runtime.registry import ToolResult

log = logging.getLogger(__name__)


class ToolPermissions(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    read: bool = True
    write: bool = False
    destructive: bool = False
    network: bool = False


class ToolPolicy(BaseModel):
    """Everything the runtime needs to execute one tool safely."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    permissions: ToolPermissions = Field(default_factory=ToolPermissions)
    timeout_seconds: float = Field(default=30.0, gt=0, le=300)
    concurrency_limit: int = Field(default=1, ge=1, le=100)
    auth_scope: str = Field(default="", max_length=120)
    max_retries: int = Field(default=0, ge=0, le=5)
    idempotency: Literal["required", "optional"] = "optional"
    requires_approval: bool = False


DEFAULT_POLICY = ToolPolicy()
MCP_DEFAULT_POLICY = ToolPolicy(permissions=ToolPermissions(read=True, network=True))


@dataclass
class ToolInvocation:
    handler: Callable[[dict[str, Any], str], Awaitable[dict[str, Any]]]
    policy: ToolPolicy = DEFAULT_POLICY
    annotations: dict[str, Any] = field(default_factory=dict)
    network_required: bool = False


class ToolExecutor:
    """Runs named invocations with one policy each and records a small trace."""

    def __init__(self) -> None:
        self._invocations: dict[str, ToolInvocation] = {}
        self._semaphores: dict[str, asyncio.Semaphore] = {}
        self._trace: list[dict[str, Any]] = []

    def register(self, name: str, invocation: ToolInvocation) -> None:
        if name in self._invocations:
            raise ValueError(f"tool {name!r} is already registered")
        self._invocations[name] = invocation
        self._semaphores[name] = asyncio.Semaphore(invocation.policy.concurrency_limit)

    @property
    def trace(self) -> list[dict[str, Any]]:
        return list(self._trace)

    def names(self) -> list[str]:
        return list(self._invocations)

    def policy(self, name: str) -> ToolPolicy | None:
        invocation = self._invocations.get(name)
        return invocation.policy if invocation else None

    def _check(self, name: str, invocation: ToolInvocation, idempotency_key: str) -> ToolResult | None:
        policy = invocation.policy
        annotations = invocation.annotations
        if policy.requires_approval:
            return ToolResult(
                ok=False,
                error_code="APPROVAL_REQUIRED",
                error_message=f"tool {name!r} requires a human approval node before execution",
            )
        if invocation.network_required and not policy.permissions.network:
            return ToolResult(ok=False, error_code="PERMISSION_DENIED", error_message="network access is not allowed")
        writes = annotations.get("destructiveHint") is True or annotations.get("readOnlyHint") is False
        if not writes and not policy.permissions.read:
            return ToolResult(ok=False, error_code="PERMISSION_DENIED", error_message="read access is not allowed")
        if annotations.get("destructiveHint") is True and not policy.permissions.destructive:
            return ToolResult(
                ok=False, error_code="PERMISSION_DENIED", error_message="destructive tool calls are not allowed"
            )
        if annotations.get("readOnlyHint") is False and not policy.permissions.write:
            return ToolResult(ok=False, error_code="PERMISSION_DENIED", error_message="write access is not allowed")
        if policy.idempotency == "required" and not idempotency_key:
            return ToolResult(
                ok=False, error_code="IDEMPOTENCY_KEY_REQUIRED", error_message="this tool requires an idempotency key"
            )
        return None

    async def execute(self, name: str, arguments: dict[str, Any], *, idempotency_key: str = "") -> ToolResult:
        invocation = self._invocations.get(name)
        if invocation is None:
            return ToolResult(ok=False, error_code="unknown_tool", error_message=f"tool {name!r} is not available")
        denied = self._check(name, invocation, idempotency_key)
        if denied is not None:
            return denied

        policy = invocation.policy
        attempts = policy.max_retries + 1
        last_error: Exception | None = None
        for attempt in range(attempts):
            started = time.monotonic()
            try:
                async with self._semaphores[name]:
                    async with asyncio.timeout(policy.timeout_seconds):
                        output = await invocation.handler(arguments, idempotency_key)
                self._trace.append(
                    {
                        "tool": name,
                        "ok": True,
                        "attempt": attempt + 1,
                        "duration_ms": int((time.monotonic() - started) * 1000),
                    }
                )
                return ToolResult(ok=True, output=output)
            except TimeoutError:
                last_error = TimeoutError(f"tool {name!r} timed out")
            except (TypeError, ValueError) as exc:
                log.warning("tool rejected input", extra={"tool": name, "error": str(exc)})
                return ToolResult(ok=False, error_code="invalid_arguments", error_message=str(exc))
            except Exception as exc:  # pragma: no cover - external tool boundary
                last_error = exc
            self._trace.append(
                {
                    "tool": name,
                    "ok": False,
                    "attempt": attempt + 1,
                    "duration_ms": int((time.monotonic() - started) * 1000),
                }
            )
            if attempt + 1 < attempts:
                await asyncio.sleep(min(0.5 * (attempt + 1), 2.0))

        code = "TOOL_TIMEOUT" if isinstance(last_error, TimeoutError) else "tool_failed"
        message = str(last_error) if isinstance(last_error, TimeoutError) else "tool execution failed"
        return ToolResult(ok=False, error_code=code, error_message=message)
