"""Tool policy enforcement tests."""

from __future__ import annotations

import asyncio
from typing import Any

import pytest

from tool_runtime.policy import ToolExecutor, ToolInvocation, ToolPermissions, ToolPolicy


async def _echo(arguments: dict[str, Any], _key: str) -> dict[str, Any]:
    return {"echo": arguments}


@pytest.mark.asyncio
async def test_destructive_tool_is_denied_by_default() -> None:
    executor = ToolExecutor()
    executor.register(
        "delete",
        ToolInvocation(
            handler=_echo,
            annotations={"destructiveHint": True, "readOnlyHint": False},
        ),
    )
    result = await executor.execute("delete", {"id": "1"})
    assert result.ok is False
    assert result.error_code == "PERMISSION_DENIED"


@pytest.mark.asyncio
async def test_destructive_tool_allowed_with_explicit_permission() -> None:
    executor = ToolExecutor()
    executor.register(
        "delete",
        ToolInvocation(
            handler=_echo,
            annotations={"destructiveHint": True, "readOnlyHint": False},
            policy=ToolPolicy(permissions=ToolPermissions(read=True, write=True, destructive=True)),
        ),
    )
    result = await executor.execute("delete", {"id": "1"})
    assert result.ok is True


@pytest.mark.asyncio
async def test_network_and_idempotency_are_enforced() -> None:
    executor = ToolExecutor()
    executor.register("fetch", ToolInvocation(handler=_echo, network_required=True))
    denied = await executor.execute("fetch", {})
    assert denied.error_code == "PERMISSION_DENIED"

    executor.register(
        "write",
        ToolInvocation(
            handler=_echo,
            policy=ToolPolicy(idempotency="required"),
        ),
    )
    missing = await executor.execute("write", {})
    assert missing.error_code == "IDEMPOTENCY_KEY_REQUIRED"
    allowed = await executor.execute("write", {}, idempotency_key="run:1")
    assert allowed.ok is True


@pytest.mark.asyncio
async def test_timeout_and_retry() -> None:
    attempts = 0

    async def slow(_arguments: dict[str, Any], _key: str) -> dict[str, Any]:
        await asyncio.sleep(0.2)
        return {}

    executor = ToolExecutor()
    executor.register("slow", ToolInvocation(handler=slow, policy=ToolPolicy(timeout_seconds=0.05)))
    timed_out = await executor.execute("slow", {})
    assert timed_out.error_code == "TOOL_TIMEOUT"

    async def flaky(_arguments: dict[str, Any], _key: str) -> dict[str, Any]:
        nonlocal attempts
        attempts += 1
        if attempts < 3:
            raise RuntimeError("transient")
        return {"ok": True}

    executor.register(
        "flaky",
        ToolInvocation(
            handler=flaky,
            policy=ToolPolicy(max_retries=2, timeout_seconds=1),
        ),
    )
    retried = await executor.execute("flaky", {})
    assert retried.ok is True
    assert attempts == 3


@pytest.mark.asyncio
async def test_read_permission_can_be_revoked() -> None:
    executor = ToolExecutor()
    executor.register(
        "reader",
        ToolInvocation(
            handler=_echo,
            annotations={"readOnlyHint": True},
            policy=ToolPolicy(permissions=ToolPermissions(read=False)),
        ),
    )
    result = await executor.execute("reader", {})
    assert result.error_code == "PERMISSION_DENIED"


@pytest.mark.asyncio
async def test_requires_approval_blocks_execution() -> None:
    executor = ToolExecutor()
    executor.register("danger", ToolInvocation(handler=_echo, policy=ToolPolicy(requires_approval=True)))
    result = await executor.execute("danger", {})
    assert result.error_code == "APPROVAL_REQUIRED"
