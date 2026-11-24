"""LangGraph adapter tests: loop, tools, checkpoints, budget, cancellation, deadline."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from typing import Any

import pytest

from agent_runtime.langgraph_runtime import LangGraphRuntime, _from_langchain, _to_langchain
from agent_runtime.spec import AgentVersionSpec, NodeContext
from checkpointing.codec import decode
from model_providers import Message, ProviderError, ToolCall
from model_providers.registry import PROVIDERS


def _spec(*, tools: bool = False, max_turns: int = 4, checkpoint: str = "on_node_end") -> AgentVersionSpec:
    data: dict[str, Any] = {
        "model": {"provider": "echo", "name": "echo-1", "credential_ref": "default"},
        "instructions": "You are a test agent.",
        "runtime": {"abi_version": 1, "image_digest": "sha256:" + "a" * 64},
        "limits": {"max_turns": max_turns, "timeout_seconds": 60},
        "checkpoint": {"mode": checkpoint},
    }
    if tools:
        data["tools"] = [{"tool_id": "add", "version": 1}]
    return AgentVersionSpec.from_mapping(data)


def _context(*, deadline: datetime | None = None) -> NodeContext:
    return NodeContext(
        org_id="org_1",
        project_id="proj_1",
        execution_id="exec_1",
        task_id="task_1",
        attempt_id="att_1",
        node_id="plan",
        agent_version_id="agtv_1",
        execution_input={"topic": "hello world"},
        deadline=deadline,
    )


async def test_agent_loop_completes_without_tools() -> None:
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(), _context())

    result = await runtime.execute({"topic": "hello world"}, _context())

    assert result.outcome == "succeeded"
    assert result.output["report"].startswith("[echo:")
    assert result.output["steps"] == 1
    assert result.model == "echo-1"
    assert result.input_tokens > 0
    assert result.checkpoint is not None


async def test_agent_loop_runs_tools_and_resumes() -> None:
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(tools=True), _context())

    result = await runtime.execute({"topic": "tool:add:[1,2,3]"}, _context())

    assert result.outcome == "succeeded"
    assert result.output["steps"] == 2
    assert result.input_tokens > 6
    checkpoint = decode(result.checkpoint or b"")
    assert checkpoint.agent_version_id == "agtv_1"
    assert len(checkpoint.state["messages"]) >= 4

    # A fresh runtime resumes the conversation instead of starting over.
    resumed = LangGraphRuntime()
    await resumed.initialize(_spec(tools=True), _context())
    await resumed.restore_state(checkpoint.model_dump())
    second = await resumed.execute({"topic": "tool:add:[1,2,3]"}, _context())
    assert second.outcome == "succeeded"
    # The final checkpoint records completed work: a retry must return the same result without another model call.
    assert second.output["steps"] == 2
    assert second.output == result.output


async def test_mid_run_checkpoint_resumes_tool_conversation() -> None:
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(tools=True), _context())
    result = await runtime.execute({"topic": "tool:add:[1,2,3]"}, _context())
    checkpoint = decode(result.checkpoint or b"")
    state = dict(checkpoint.state)
    state.pop("completed", None)
    state.pop("output", None)
    from checkpointing.codec import Checkpoint

    mid = Checkpoint(agent_version_id=checkpoint.agent_version_id, state=state)
    resumed = LangGraphRuntime()
    await resumed.initialize(_spec(tools=True), _context())
    await resumed.restore_state(mid.model_dump())
    second = await resumed.execute({"topic": "tool:add:[1,2,3]"}, _context())
    assert second.outcome == "succeeded"
    assert second.output["steps"] == 3


async def test_tool_pending_checkpoint_resumes_at_tools() -> None:
    from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, message_to_dict

    from checkpointing.codec import Checkpoint

    messages = [
        SystemMessage(content="You are a test agent."),
        HumanMessage(content='{"input": {"topic": "tool:add:[1,2,3]"}, "params": {}}'),
        AIMessage(
            content="",
            tool_calls=[{"name": "add", "args": {"numbers": [1, 2, 3]}, "id": "call_echo_1", "type": "tool_call"}],
        ),
    ]
    checkpoint = Checkpoint(
        agent_version_id="agtv_1",
        state={
            "messages": [message_to_dict(message) for message in messages],
            "steps": 1,
            "phase": "tools_pending",
            "completed": False,
        },
    )
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(tools=True), _context())
    await runtime.restore_state(checkpoint.model_dump())
    result = await runtime.execute({"topic": "tool:add:[1,2,3]"}, _context())
    assert result.outcome == "succeeded"
    # The tool node is entered directly; only the final model turn runs.
    assert result.output["steps"] == 2


async def test_checkpoint_disabled_by_spec() -> None:
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(checkpoint="none"), _context())
    result = await runtime.execute({"topic": "x"}, _context())
    assert result.outcome == "succeeded"
    assert result.checkpoint is None


async def test_max_turns_is_enforced() -> None:
    class AlwaysTool:
        name = "always"

        async def complete(
            self,
            messages: list[Any],
            config: Any,
            *,
            tools: list[Any] | None = None,
            response_schema: dict[str, Any] | None = None,
        ) -> Any:
            from model_providers.base import Completion, ToolCall

            return Completion(
                text="again",
                model="always-1",
                input_tokens=1,
                output_tokens=1,
                tool_calls=(ToolCall(id="call_loop", name="add", arguments={"numbers": [1]}),),
                finish_reason="tool_calls",
            )

    PROVIDERS["always"] = AlwaysTool  # type: ignore[assignment]
    try:
        runtime = LangGraphRuntime()
        spec = _spec(tools=True, max_turns=2)
        spec = spec.model_copy(
            update={"model": spec.model.model_copy(update={"provider": "always", "name": "always-1"})}
        )
        await runtime.initialize(spec, _context())
        result = await runtime.execute({"topic": "loop"}, _context())
        assert result.outcome == "failed"
        assert result.error is not None
        assert result.error.code == "MAX_TURNS_EXCEEDED"
    finally:
        PROVIDERS.pop("always", None)


async def test_cancellation_stops_the_loop() -> None:
    runtime = LangGraphRuntime()
    await runtime.initialize(_spec(), _context())
    await runtime.cancel()

    result = await runtime.execute({"topic": "x"}, _context())

    assert result.outcome == "cancelled"
    assert result.error is not None
    assert result.error.code == "CANCELLED"


async def test_deadline_exceeded_is_a_timeout_failure() -> None:
    runtime = LangGraphRuntime()
    past = datetime.now(UTC) - timedelta(seconds=1)
    await runtime.initialize(_spec(), _context(deadline=past))

    result = await runtime.execute({"topic": "x"}, _context(deadline=past))

    assert result.outcome == "failed"
    assert result.error is not None
    assert result.error.code == "TASK_TIMEOUT"
    assert result.error.retryable is True


async def test_unknown_tool_binding_fails_initialization() -> None:
    runtime = LangGraphRuntime()
    spec = _spec(tools=True)
    spec = spec.model_copy(update={"tools": (spec.tools[0].model_copy(update={"tool_id": "nope"}),)})
    with pytest.raises(ProviderError, match="not available"):
        await runtime.initialize(spec, _context())


def test_tool_call_signatures_round_trip_through_langchain() -> None:
    message = Message(
        role="assistant",
        content="",
        tool_calls=(ToolCall(id="call_1", name="add", arguments={"numbers": [1]}, signature="sig-123"),),
    )

    converted = _to_langchain([message])
    assert converted[0].additional_kwargs["thought_signatures"] == {"call_1": "sig-123"}

    restored = _from_langchain(converted)
    assert restored[0].tool_calls[0].signature == "sig-123"
