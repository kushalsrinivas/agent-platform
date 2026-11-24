"""Deterministic provider for tests and offline development.

It supports the full protocol surface — streaming, tool calls and structured outputs — so the LangGraph adapter
is exercised without network access or vendor credentials.
"""

from __future__ import annotations

import hashlib
import json
from collections.abc import AsyncIterator
from typing import Any

from model_providers.base import (
    Completion,
    Message,
    ProviderCapabilities,
    ProviderConfig,
    StreamEvent,
    ToolCall,
    ToolSpec,
    normalise_usage,
)

ECHO_PREFIX = "[echo:"
MARKER = "tool:add:"
DELEGATE_MARKER = "tool:delegate:"
DELEGATE_AGENT_MARKER = "tool:delegate_agent:"
MCP_MARKER = "tool:mcp:"


def _find_topic(value: Any) -> str | None:
    """Locate the first string ``topic`` field in a nested payload."""
    if isinstance(value, dict):
        topic = value.get("topic")
        if isinstance(topic, str):
            return topic
        for item in value.values():
            found = _find_topic(item)
            if found is not None:
                return found
    if isinstance(value, list):
        for item in value:
            found = _find_topic(item)
            if found is not None:
                return found
    return None


class EchoProvider:
    name = "echo"
    capabilities = ProviderCapabilities(streaming=True, tools=True, structured=True)

    def _final_text(self, messages: list[Message]) -> str:
        last_user = next((m.content for m in reversed(messages) if m.role == "user"), "")
        digest = hashlib.sha256("\n".join(f"{m.role}:{m.content}" for m in messages).encode()).hexdigest()[:12]
        tool_results = [m.content for m in messages if m.role == "tool"]
        suffix = ""
        if tool_results:
            suffix = " | tools=" + json.dumps(tool_results, sort_keys=True)
        return f"{ECHO_PREFIX}{digest}] {last_user}{suffix}"

    def _requested_tool(self, last_user: str) -> list[int] | None:
        """Return addendums when the payload asks for the ``add`` tool.

        The user payload is JSON (``{"input": ..., "params": ...}``); the offline path may also send a bare
        ``tool:add:[1,2]`` string. Both forms are deterministic and side-effect free.
        """
        candidate = last_user
        try:
            payload = json.loads(last_user)
            topic = _find_topic(payload)
            if topic is not None:
                candidate = topic
        except (json.JSONDecodeError, TypeError):
            candidate = last_user
        if not candidate.startswith(MARKER):
            return None
        try:
            numbers = json.loads(candidate[len(MARKER) :])
        except json.JSONDecodeError:
            return None
        return numbers if isinstance(numbers, list) and numbers else None

    async def complete(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> Completion:
        text = self._final_text(messages)
        last_user = next((m.content for m in reversed(messages) if m.role == "user"), "")

        candidate = last_user
        try:
            payload = json.loads(last_user)
            topic = _find_topic(payload)
            if topic is not None:
                candidate = topic
        except (json.JSONDecodeError, TypeError):
            candidate = last_user

        tool_calls: tuple[ToolCall, ...] = ()
        finish_reason = "stop"
        tool_pending = not any(m.role == "tool" for m in messages)
        if tools and tool_pending and any(tool.name == "add" for tool in tools):
            numbers = self._requested_tool(last_user)
            if numbers is not None:
                tool_calls = (ToolCall(id="call_echo_1", name="add", arguments={"numbers": numbers}),)
                finish_reason = "tool_calls"
        if tools and tool_pending and candidate.startswith(DELEGATE_AGENT_MARKER):
            try:
                arguments = json.loads(candidate[len(DELEGATE_AGENT_MARKER) :])
            except json.JSONDecodeError:
                arguments = None
            if isinstance(arguments, dict) and any(tool.name == "delegate_task" for tool in tools):
                tool_calls = (ToolCall(id="call_echo_delegate_agent", name="delegate_task", arguments=arguments),)
                finish_reason = "tool_calls"
        if tools and tool_pending and candidate.startswith(DELEGATE_MARKER):
            try:
                arguments = json.loads(candidate[len(DELEGATE_MARKER) :])
            except json.JSONDecodeError:
                arguments = None
            if isinstance(arguments, dict) and any(tool.name == "delegate_tasks" for tool in tools):
                tool_calls = (ToolCall(id="call_echo_delegate", name="delegate_tasks", arguments=arguments),)
                finish_reason = "tool_calls"
        if tools and tool_pending and candidate.startswith(MCP_MARKER):
            try:
                request = json.loads(candidate[len(MCP_MARKER) :])
            except json.JSONDecodeError:
                request = None
            if isinstance(request, dict):
                server = str(request.get("server", ""))
                tool = str(request.get("tool", ""))
                tool_name = f"mcp__{server}__{tool}"
                if any(item.name == tool_name for item in tools):
                    raw_arguments = request.get("arguments")
                    mcp_arguments: dict[str, Any] = raw_arguments if isinstance(raw_arguments, dict) else {}
                    tool_calls = (ToolCall(id="call_echo_mcp", name=tool_name, arguments=mcp_arguments),)
                    finish_reason = "tool_calls"
        if not tool_calls and response_schema is not None:
            # Structured mode returns a JSON object that satisfies any reasonable schema shape.
            text = json.dumps({"result": text}, sort_keys=True)

        usage = {"input_tokens": sum(len(m.content) for m in messages), "output_tokens": len(text)}
        input_tokens, output_tokens = normalise_usage(usage)
        return Completion(
            text=text,
            model=config.model,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tool_calls=tool_calls,
            finish_reason=finish_reason,
            raw_usage=usage,
        )

    async def stream(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> AsyncIterator[StreamEvent]:
        completion = await self.complete(messages, config, tools=tools, response_schema=response_schema)
        for index in range(0, len(completion.text), 16):
            yield StreamEvent(delta=completion.text[index : index + 16])
        yield StreamEvent(
            delta="",
            finish=True,
            input_tokens=completion.input_tokens,
            output_tokens=completion.output_tokens,
        )
