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
        return f"{ECHO_PREFIX}{digest}] {last_user}"

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

        tool_calls: tuple[ToolCall, ...] = ()
        finish_reason = "stop"
        tool_pending = not any(m.role == "tool" for m in messages)
        if tools and tool_pending and any(tool.name == "add" for tool in tools):
            numbers = self._requested_tool(last_user)
            if numbers is not None:
                tool_calls = (ToolCall(id="call_echo_1", name="add", arguments={"numbers": numbers}),)
                finish_reason = "tool_calls"
        elif response_schema is not None:
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
