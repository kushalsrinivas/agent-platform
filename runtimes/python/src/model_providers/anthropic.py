"""Anthropic Messages API adapter.

Plain HTTP against the documented wire format; no vendor SDK. Tool results are sent as user blocks with
``tool_result``, and streaming uses Anthropic's SSE event stream.
"""

from __future__ import annotations

import json
from collections.abc import AsyncIterator
from typing import Any

import httpx

from model_providers.base import (
    Completion,
    Message,
    ProviderCapabilities,
    ProviderConfig,
    ProviderError,
    StreamEvent,
    ToolCall,
    ToolSpec,
    normalise_usage,
)

DEFAULT_BASE_URL = "https://api.anthropic.com"
ANTHROPIC_VERSION = "2023-06-01"


def _payload_messages(messages: list[Message]) -> tuple[str, list[dict[str, Any]]]:
    """Split the conversation into a top-level system prompt and role-tagged content blocks."""
    system_parts = [m.content for m in messages if m.role == "system"]
    conversation: list[dict[str, Any]] = []
    for message in messages:
        if message.role == "system":
            continue
        if message.role == "tool":
            conversation.append(
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": message.tool_call_id or "",
                            "content": message.content or "ok",
                        }
                    ],
                }
            )
            continue
        if message.role == "assistant" and message.tool_calls:
            blocks: list[dict[str, Any]] = []
            if message.content:
                blocks.append({"type": "text", "text": message.content})
            blocks.extend(
                {
                    "type": "tool_use",
                    "id": call.id,
                    "name": call.name,
                    "input": call.arguments,
                }
                for call in message.tool_calls
            )
            conversation.append({"role": "assistant", "content": blocks})
            continue
        conversation.append({"role": message.role, "content": [{"type": "text", "text": message.content}]})
    system = "\n\n".join(system_parts)
    return system, conversation


def _tools_payload(tools: list[ToolSpec]) -> list[dict[str, Any]]:
    return [
        {
            "name": tool.name,
            "description": tool.description,
            "input_schema": tool.parameters or {"type": "object", "properties": {}},
        }
        for tool in tools
    ]


class AnthropicProvider:
    name = "anthropic"
    capabilities = ProviderCapabilities(streaming=True, tools=True, structured=False)

    def __init__(self, client: httpx.AsyncClient | None = None) -> None:
        self._client = client

    def _endpoint(self, config: ProviderConfig, *, stream: bool = False) -> str:
        base = (config.base_url or DEFAULT_BASE_URL).rstrip("/")
        return f"{base}/v1/messages" + ("?stream=true" if stream else "")

    def _headers(self, config: ProviderConfig) -> dict[str, str]:
        if config.api_key is None:
            raise ProviderError("config", "api_key is required for the Anthropic provider")
        return {
            "x-api-key": config.api_key.get_secret_value(),
            "anthropic-version": ANTHROPIC_VERSION,
            "content-type": "application/json",
        }

    def _body(
        self,
        messages: list[Message],
        config: ProviderConfig,
        tools: list[ToolSpec] | None,
        *,
        stream: bool = False,
    ) -> dict[str, Any]:
        system, conversation = _payload_messages(messages)
        body: dict[str, Any] = {
            "model": config.model,
            "max_tokens": config.max_tokens,
            "temperature": config.temperature,
            "messages": conversation,
        }
        if system:
            body["system"] = system
        if tools:
            body["tools"] = _tools_payload(tools)
        if stream:
            body["stream"] = True
        return body

    async def _post(
        self, url: str, headers: dict[str, str], body: dict[str, Any], config: ProviderConfig
    ) -> httpx.Response:
        try:
            if self._client is not None:
                return await self._client.post(url, json=body, headers=headers)
            async with httpx.AsyncClient(timeout=config.timeout_seconds) as client:
                return await client.post(url, json=body, headers=headers)
        except httpx.TimeoutException as exc:
            raise ProviderError("timeout", "model provider timed out", retryable=True) from exc
        except httpx.HTTPError as exc:
            raise ProviderError("transport", "model provider request failed", retryable=True) from exc

    @staticmethod
    def _raise_for_status(response: httpx.Response) -> None:
        if response.status_code == 429:
            raise ProviderError(
                "rate_limited", "model provider rate limit exceeded", retryable=True, status_code=429, rate_limited=True
            )
        if response.status_code >= 500:
            raise ProviderError(
                "provider_unavailable",
                f"model provider returned {response.status_code}",
                retryable=True,
                status_code=response.status_code,
            )
        if response.status_code >= 400:
            raise ProviderError(
                "provider_rejected", f"model provider returned {response.status_code}", status_code=response.status_code
            )

    async def complete(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> Completion:
        # Structured outputs are not a first-class capability of the Messages API; prompt-based JSON is left to
        # the agent instead of being forced through this adapter.
        if response_schema is not None:
            raise ProviderError("unsupported_capability", "the Anthropic adapter does not implement structured outputs")
        url = self._endpoint(config)
        headers = self._headers(config)
        body = self._body(messages, config, tools)
        response = await self._post(url, headers, body, config)
        self._raise_for_status(response)

        try:
            data = response.json()
            text_parts: list[str] = []
            tool_calls: list[ToolCall] = []
            for block in data.get("content", []):
                if block.get("type") == "text":
                    text_parts.append(str(block.get("text", "")))
                elif block.get("type") == "tool_use":
                    tool_calls.append(
                        ToolCall(
                            id=str(block.get("id", "")),
                            name=str(block.get("name", "")),
                            arguments=dict(block.get("input", {})),
                        )
                    )
            usage = data.get("usage", {})
            finish_reason = "tool_calls" if tool_calls else "stop"
        except (TypeError, ValueError) as exc:
            raise ProviderError("invalid_response", "model provider response was not understood") from exc

        input_tokens, output_tokens = normalise_usage(usage)
        return Completion(
            text="".join(text_parts),
            model=str(data.get("model", config.model)),
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tool_calls=tuple(tool_calls),
            finish_reason=finish_reason,
            raw_usage={k: int(v) for k, v in usage.items() if isinstance(v, int)},
        )

    async def stream(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> AsyncIterator[StreamEvent]:
        if response_schema is not None:
            raise ProviderError("unsupported_capability", "the Anthropic adapter does not implement structured outputs")
        url = self._endpoint(config, stream=True)
        headers = self._headers(config)
        headers["Accept"] = "text/event-stream"
        body = self._body(messages, config, tools, stream=True)

        client = self._client
        owns_client = client is None
        if client is None:
            client = httpx.AsyncClient(timeout=config.timeout_seconds)
        try:
            try:
                response = await client.send(client.build_request("POST", url, json=body, headers=headers), stream=True)
            except httpx.TimeoutException as exc:
                raise ProviderError("timeout", "model provider timed out", retryable=True) from exc
            except httpx.HTTPError as exc:
                raise ProviderError("transport", "model provider request failed", retryable=True) from exc
            self._raise_for_status(response)
            try:
                async for line in response.aiter_lines():
                    if not line.startswith("data:"):
                        continue
                    payload = line[len("data:") :].strip()
                    if not payload:
                        continue
                    event = json.loads(payload)
                    event_type = event.get("type")
                    if event_type == "content_block_delta":
                        delta = event.get("delta", {})
                        if delta.get("type") == "text_delta":
                            yield StreamEvent(delta=str(delta.get("text", "")))
                    elif event_type == "message_delta" and "usage" in event:
                        input_tokens, output_tokens = normalise_usage(event["usage"])
                        yield StreamEvent(delta="", finish=True, input_tokens=input_tokens, output_tokens=output_tokens)
                        return
            except json.JSONDecodeError as exc:
                raise ProviderError("invalid_response", "model provider stream was not understood") from exc
            finally:
                await response.aclose()
            yield StreamEvent(delta="", finish=True)
        finally:
            if owns_client:
                await client.aclose()
