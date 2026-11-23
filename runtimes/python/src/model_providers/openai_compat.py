"""OpenAI-compatible chat completions over plain HTTP.

Compatible with OpenAI, vLLM, Ollama's OpenAI endpoint and most gateways. No vendor SDK is imported; the wire
format is stable and keeps the runtime image small. Streaming uses SSE, structured outputs use ``response_format``
and tools use the function-calling shape.
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


def _payload_messages(messages: list[Message]) -> list[dict[str, Any]]:
    payload: list[dict[str, Any]] = []
    for message in messages:
        item: dict[str, Any] = {"role": message.role, "content": message.content}
        if message.name:
            item["name"] = message.name
        if message.tool_call_id:
            item["tool_call_id"] = message.tool_call_id
        if message.tool_calls:
            item["tool_calls"] = [
                {
                    "id": call.id,
                    "type": "function",
                    "function": {
                        "name": call.name,
                        "arguments": json.dumps(call.arguments, sort_keys=True),
                    },
                }
                for call in message.tool_calls
            ]
        payload.append(item)
    return payload


def _tools_payload(tools: list[ToolSpec]) -> list[dict[str, Any]]:
    return [
        {
            "type": "function",
            "function": {
                "name": tool.name,
                "description": tool.description,
                "parameters": tool.parameters or {"type": "object", "properties": {}},
            },
        }
        for tool in tools
    ]


class OpenAICompatibleProvider:
    name = "openai_compatible"
    capabilities = ProviderCapabilities(streaming=True, tools=True, structured=True)

    def __init__(self, client: httpx.AsyncClient | None = None) -> None:
        self._client = client

    def _endpoint(self, config: ProviderConfig, *, stream: bool = False) -> str:
        if not config.base_url:
            raise ProviderError("config", "base_url is required for an OpenAI-compatible provider")
        base = config.base_url.rstrip("/")
        return f"{base}/chat/completions" + ("?stream=true" if stream else "")

    def _headers(self, config: ProviderConfig) -> dict[str, str]:
        if config.api_key is None:
            raise ProviderError("config", "api_key is required for an OpenAI-compatible provider")
        return {"Authorization": f"Bearer {config.api_key.get_secret_value()}"}

    def _body(
        self,
        messages: list[Message],
        config: ProviderConfig,
        tools: list[ToolSpec] | None,
        response_schema: dict[str, Any] | None,
        *,
        stream: bool = False,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "model": config.model,
            "messages": _payload_messages(messages),
            "temperature": config.temperature,
            "max_tokens": config.max_tokens,
        }
        if tools:
            body["tools"] = _tools_payload(tools)
        if response_schema is not None:
            body["response_format"] = {
                "type": "json_schema",
                "json_schema": {"name": "output", "schema": response_schema, "strict": False},
            }
        if stream:
            body["stream"] = True
            body["stream_options"] = {"include_usage": True}
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
                "rate_limited",
                "model provider rate limit exceeded",
                retryable=True,
                status_code=429,
                rate_limited=True,
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
                "provider_rejected",
                f"model provider returned {response.status_code}",
                status_code=response.status_code,
            )

    async def complete(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> Completion:
        url = self._endpoint(config)
        headers = self._headers(config)
        body = self._body(messages, config, tools, response_schema)
        response = await self._post(url, headers, body, config)
        self._raise_for_status(response)

        try:
            data = response.json()
            choice = data["choices"][0]
            message = choice["message"]
            text = message.get("content") or ""
            raw_calls = message.get("tool_calls") or []
            tool_calls = tuple(
                ToolCall(
                    id=str(call["id"]),
                    name=str(call["function"]["name"]),
                    arguments=json.loads(call["function"].get("arguments") or "{}"),
                )
                for call in raw_calls
            )
            usage = data.get("usage", {})
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise ProviderError("invalid_response", "model provider response was not understood") from exc

        input_tokens, output_tokens = normalise_usage(usage)
        return Completion(
            text=str(text),
            model=str(data.get("model", config.model)),
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tool_calls=tool_calls,
            finish_reason=str(choice.get("finish_reason") or "") or None,
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
        url = self._endpoint(config, stream=True)
        headers = self._headers(config)
        headers["Accept"] = "text/event-stream"
        body = self._body(messages, config, tools, response_schema, stream=True)

        client = self._client
        owns_client = client is None
        if client is None:
            client = httpx.AsyncClient(timeout=config.timeout_seconds)
        try:
            try:
                response = await client.send(
                    client.build_request("POST", url, json=body, headers=headers),
                    stream=True,
                )
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
                    if not payload or payload == "[DONE]":
                        continue
                    chunk = json.loads(payload)
                    choices = chunk.get("choices") or []
                    delta = choices[0].get("delta", {}) if choices else {}
                    text = delta.get("content") or ""
                    if text:
                        yield StreamEvent(delta=str(text))
                    usage = chunk.get("usage")
                    if usage:
                        input_tokens, output_tokens = normalise_usage(usage)
                        yield StreamEvent(
                            delta="",
                            finish=True,
                            input_tokens=input_tokens,
                            output_tokens=output_tokens,
                        )
                        return
            except json.JSONDecodeError as exc:
                raise ProviderError("invalid_response", "model provider stream was not understood") from exc
            finally:
                await response.aclose()
            yield StreamEvent(delta="", finish=True)
        finally:
            if owns_client:
                await client.aclose()
