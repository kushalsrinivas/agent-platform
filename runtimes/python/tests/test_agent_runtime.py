"""Agent runtime contract tests: spec parsing, echo runtime, cancellation."""

from __future__ import annotations

import pytest

from agent_runtime.runtime import EchoAgentRuntime
from agent_runtime.spec import AgentVersionSpec, NodeContext, NodeResult


def _spec() -> AgentVersionSpec:
    return AgentVersionSpec.from_mapping(
        {
            "model": {"provider": "echo", "name": "echo-1", "credential_ref": "default"},
            "instructions": "You are a test agent.",
            "tools": [{"tool_id": "add", "version": 1, "config": {}}],
            "runtime": {"abi_version": 1, "image_digest": "sha256:" + "a" * 64},
            "limits": {"max_turns": 4, "timeout_seconds": 60},
            "checkpoint": {"mode": "on_node_end", "require_checkpoint": False},
        }
    )


def _context() -> NodeContext:
    return NodeContext(
        org_id="org_1",
        project_id="proj_1",
        execution_id="exec_1",
        task_id="task_1",
        attempt_id="att_1",
        node_id="plan",
        agent_version_id="agtv_1",
        execution_input={"topic": "hello"},
    )


def test_spec_parses_control_plane_shape() -> None:
    spec = _spec()
    assert spec.model.provider == "echo"
    assert spec.model.name == "echo-1"
    assert spec.instructions == "You are a test agent."
    assert spec.tools[0].tool_id == "add"
    assert spec.runtime.abi_version == 1
    assert spec.limits.max_turns == 4
    assert spec.checkpoint.mode == "on_node_end"


def test_spec_preserves_unknown_fields() -> None:
    spec = AgentVersionSpec.from_mapping(
        {
            "model": {"provider": "echo", "name": "echo-1"},
            "instructions": "x",
            "future_field": {"anything": True},
        }
    )
    assert spec.model_extra is not None
    assert spec.model_extra["future_field"] == {"anything": True}


async def test_echo_runtime_executes_the_contract() -> None:
    runtime = EchoAgentRuntime()
    spec = _spec()
    context = _context()

    await runtime.initialize(spec, context)
    result = await runtime.execute({"topic": "hello"}, context)
    state = await runtime.get_state()
    await runtime.shutdown()

    assert isinstance(result, NodeResult)
    assert result.outcome == "succeeded"
    assert result.output["report"].startswith("[echo]")
    assert result.steps == 1
    assert state == {"done": True}


async def test_echo_runtime_restore_is_a_noop() -> None:
    runtime = EchoAgentRuntime()
    await runtime.initialize(_spec(), _context())
    await runtime.restore_state({"state": {"done": True}})
    result = await runtime.execute({"topic": "again"}, _context())
    assert result.outcome == "succeeded"


async def test_cancelled_runtime_returns_cancelled() -> None:
    runtime = EchoAgentRuntime()
    await runtime.initialize(_spec(), _context())
    await runtime.cancel()
    result = await runtime.execute({"topic": "x"}, _context())
    assert result.outcome == "cancelled"
    assert result.error is not None
    assert result.error.code == "CANCELLED"


def test_unknown_tool_binding_is_rejected_at_initialization() -> None:
    import asyncio

    from agent_runtime.langgraph_runtime import LangGraphRuntime
    from model_providers.base import ProviderError

    runtime = LangGraphRuntime()
    spec = AgentVersionSpec.from_mapping(
        {
            "model": {"provider": "echo", "name": "echo-1"},
            "instructions": "x",
            "tools": [{"tool_id": "does_not_exist", "version": 1}],
        }
    )
    with pytest.raises(ProviderError, match="not available"):
        import asyncio

        asyncio.get_event_loop_policy().new_event_loop().run_until_complete(runtime.initialize(spec, _context()))
