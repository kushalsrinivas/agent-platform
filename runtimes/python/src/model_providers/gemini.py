"""Google Gemini adapter.

Uses ``v1beta`` ``generateContent`` with ``functionDeclarations`` for tools, ``responseMimeType``/``responseSchema``
for structured outputs and SSE ``streamGenerateContent`` for streaming.
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

DEFAULT_BASE_URL = "https://generativelanguage.googleapis.com/v1beta"


def _contents(messages: list[Message]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """Return (system parts, contents). Gemini tags the assistant as ``model`` and tool results as user parts."""
    system_parts: list[dict[str, Any]] = []
    contents: list[dict[str, Any]] = []
    for message in messages:
        if message.role == "system":
            system_parts.append({"text": message.content})
            continue
        if message.role == "tool":
            contents.append(
                {
                    "role": "user",
                    "parts": [
                        {
                            "functionResponse": {
                                "name": message.name or "tool",
                                "response": {"result": message.content or "ok"},
                            }
                        }
                    ],
                }
            )
            continue
        if message.role == "assistant" and message.tool_calls:
            parts: list[dict[str, Any]] = []
            if message.content:
                parts.append({"text": message.content})
            for call in message.tool_calls:
                part: dict[str, Any] = {"functionCall": {"name": call.name, "args": call.arguments}}
                if call.signature:
                    part["thoughtSignature"] = call.signature
                parts.append(part)
            contents.append({"role": "model", "parts": parts})
            continue
        contents.append({"role": "user" if message.role == "user" else "model", "parts": [{"text": message.content}]})
    return system_parts, contents


def _tools_payload(tools: list[ToolSpec]) -> list[dict[str, Any]]:
    declarations = [
        {
            "name": tool.name,
            "description": tool.description,
            "parameters": tool.parameters or {"type": "object", "properties": {}},
        }
        for tool in tools
    ]
    return [{"functionDeclarations": declarations}]


class GeminiProvider:
    name = "gemini"
    capabilities = ProviderCapabilities(streaming=True, tools=True, structured=True)

    def __init__(self, client: httpx.AsyncClient | None = None) -> None:
        self._client = client

    def _endpoint(self, config: ProviderConfig, *, stream: bool = False) -> str:
        base = (config.base_url or DEFAULT_BASE_URL).rstrip("/")
        action = "streamGenerateContent" if stream else "generateContent"
        suffix = "?alt=sse" if stream else ""
        return f"{base}/models/{config.model}:{action}{suffix}"

    def _headers(self, config: ProviderConfig) -> dict[str, str]:
        if config.api_key is None:
            raise ProviderError("config", "api_key is required for the Gemini provider")
        return {"x-goog-api-key": config.api_key.get_secret_value(), "content-type": "application/json"}

    def _body(
        self,
        messages: list[Message],
        config: ProviderConfig,
        tools: list[ToolSpec] | None,
        response_schema: dict[str, Any] | None,
    ) -> dict[str, Any]:
        system_parts, contents = _contents(messages)
        generation_config: dict[str, Any] = {
            "temperature": config.temperature,
            "maxOutputTokens": config.max_tokens,
        }
        if response_schema is not None:
            generation_config["responseMimeType"] = "application/json"
            generation_config["responseSchema"] = response_schema
        body: dict[str, Any] = {"contents": contents, "generationConfig": generation_config}
        if system_parts:
            body["systemInstruction"] = {"parts": system_parts}
        if tools:
            body["tools"] = _tools_payload(tools)
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

    @staticmethod
    def _parse_completion(data: dict[str, Any], config: ProviderConfig) -> Completion:
        try:
            candidates = data.get("candidates") or []
            parts = (candidates[0].get("content") or {}).get("parts") if candidates else []
            text_parts: list[str] = []
            tool_calls: list[ToolCall] = []
            for part in parts or []:
                if "text" in part:
                    text_parts.append(str(part["text"]))
                elif "functionCall" in part:
                    call = part["functionCall"]
                    tool_calls.append(
                        ToolCall(
                            id=f"call_{len(tool_calls) + 1}",
                            name=str(call.get("name", "")),
                            arguments=dict(call.get("args", {})),
                            signature=str(part["thoughtSignature"]) if part.get("thoughtSignature") else None,
                        )
                    )
            usage = data.get("usageMetadata", {})
            finish_reason = "tool_calls" if tool_calls else "stop"
        except (TypeError, ValueError, IndexError) as exc:
            raise ProviderError("invalid_response", "model provider response was not understood") from exc

        input_tokens, output_tokens = normalise_usage(usage)
        # Gemini reports thinking tokens separately; they are billable output tokens.
        output_tokens += int(usage.get("thoughtsTokenCount", 0) or 0)
        return Completion(
            text="".join(text_parts),
            model=str(data.get("modelVersion", config.model)),
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tool_calls=tuple(tool_calls),
            finish_reason=finish_reason,
            raw_usage={k: int(v) for k, v in usage.items() if isinstance(v, int)},
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
        except ValueError as exc:
            raise ProviderError("invalid_response", "model provider response was not understood") from exc
        return self._parse_completion(data, config)

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
        body = self._body(messages, config, tools, response_schema)

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
                    chunk = json.loads(payload)
                    candidates = chunk.get("candidates") or []
                    parts = (candidates[0].get("content") or {}).get("parts") if candidates else []
                    for part in parts or []:
                        if "text" in part:
                            yield StreamEvent(delta=str(part["text"]))
                    usage = chunk.get("usageMetadata")
                    if usage:
                        input_tokens, output_tokens = normalise_usage(usage)
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
