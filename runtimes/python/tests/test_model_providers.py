"""Model provider adapter tests.

Every remote provider is exercised through ``httpx.MockTransport``: no network, no credentials, deterministic
coverage of the wire formats, usage extraction, error normalisation, rate limits, timeouts and streaming.
"""

from __future__ import annotations

import json
from typing import Any

import httpx
import pytest

from model_providers import (
    AnthropicProvider,
    EchoProvider,
    GeminiProvider,
    Message,
    OpenAICompatibleProvider,
    ProviderConfig,
    ProviderError,
    ToolSpec,
)


def _config(**overrides: Any) -> ProviderConfig:
    base: dict[str, Any] = {
        "model": "test-model",
        "base_url": "https://provider.invalid/v1",
        "api_key": "test-key",
        "timeout_seconds": 5.0,
    }
    base.update(overrides)
    return ProviderConfig.model_validate(base)


async def test_echo_provider_is_deterministic() -> None:
    provider = EchoProvider()
    messages = [Message(role="user", content="ping")]

    first = await provider.complete(messages, ProviderConfig())
    second = await provider.complete(messages, ProviderConfig())

    assert first == second
    assert first.text.endswith("ping")
    assert first.text.startswith("[echo:")
    assert first.input_tokens > 0


async def test_echo_provider_emits_tool_call_once() -> None:
    provider = EchoProvider()
    tools = [ToolSpec(name="add", description="sum", parameters={"type": "object"})]
    config = ProviderConfig()

    first = await provider.complete(
        [Message(role="user", content=json.dumps({"input": {"topic": "tool:add:[1,2]"}}))],
        config,
        tools=tools,
    )
    assert first.finish_reason == "tool_calls"
    assert first.tool_calls[0].name == "add"
    assert first.tool_calls[0].arguments == {"numbers": [1, 2]}

    after_tool = await provider.complete(
        [
            Message(role="user", content=json.dumps({"input": {"topic": "tool:add:[1,2]"}})),
            Message(role="assistant", content="", tool_calls=first.tool_calls),
            Message(role="tool", content="{}", tool_call_id=first.tool_calls[0].id, name="add"),
        ],
        config,
        tools=tools,
    )
    assert after_tool.tool_calls == ()
    assert after_tool.finish_reason == "stop"


async def test_openai_compatible_provider_parses_response_and_usage() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["authorization"] == "Bearer test-key"
        body = json.loads(request.content)
        assert body["model"] == "test-model"
        return httpx.Response(
            200,
            json={
                "model": "test-model",
                "choices": [{"message": {"content": "pong"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 3, "completion_tokens": 1},
            },
        )

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = OpenAICompatibleProvider(client)
    completion = await provider.complete([Message(role="user", content="ping")], _config())

    assert completion.text == "pong"
    assert completion.input_tokens == 3
    assert completion.output_tokens == 1
    assert completion.finish_reason == "stop"
    await client.aclose()


async def test_openai_compatible_provider_sends_tools_and_structured_output() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        assert body["tools"][0]["function"]["name"] == "add"
        assert body["response_format"]["type"] == "json_schema"
        assert body["response_format"]["json_schema"]["schema"]["type"] == "object"
        return httpx.Response(
            200,
            json={
                "model": "test-model",
                "choices": [
                    {
                        "message": {
                            "content": "",
                            "tool_calls": [
                                {
                                    "id": "call_1",
                                    "function": {"name": "add", "arguments": '{"numbers": [1, 2, 3]}'},
                                }
                            ],
                        },
                        "finish_reason": "tool_calls",
                    }
                ],
                "usage": {"prompt_tokens": 10, "completion_tokens": 2},
            },
        )

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = OpenAICompatibleProvider(client)
    completion = await provider.complete(
        [Message(role="user", content="add")],
        _config(),
        tools=[ToolSpec(name="add", description="sum", parameters={"type": "object"})],
        response_schema={"type": "object"},
    )

    assert completion.tool_calls[0].name == "add"
    assert completion.tool_calls[0].arguments == {"numbers": [1, 2, 3]}
    await client.aclose()


async def test_openai_compatible_provider_normalises_errors() -> None:
    cases: list[tuple[int, str, bool]] = [
        (429, "rate_limited", True),
        (500, "provider_unavailable", True),
        (400, "provider_rejected", False),
    ]
    for status, code, retryable in cases:
        client = httpx.AsyncClient(
            transport=httpx.MockTransport(lambda request, status=status: httpx.Response(status, json={}))
        )
        provider = OpenAICompatibleProvider(client)
        with pytest.raises(ProviderError) as exc_info:
            await provider.complete([Message(role="user", content="ping")], _config())
        assert exc_info.value.code == code
        assert exc_info.value.retryable is retryable
        assert exc_info.value.status_code == status
        await client.aclose()


async def test_openai_compatible_provider_times_out() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("timed out")

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = OpenAICompatibleProvider(client)
    with pytest.raises(ProviderError, match="timed out") as exc_info:
        await provider.complete([Message(role="user", content="ping")], _config())
    assert exc_info.value.retryable is True
    await client.aclose()


async def test_openai_compatible_provider_streams_deltas_and_usage() -> None:
    lines = [
        'data: {"choices": [{"delta": {"content": "Hel"}}]}',
        'data: {"choices": [{"delta": {"content": "lo"}}]}',
        'data: {"choices": [{"delta": {}}], "usage": {"prompt_tokens": 2, "completion_tokens": 2}}',
    ]
    payload = "\n".join(lines) + "\n"

    client = httpx.AsyncClient(
        transport=httpx.MockTransport(lambda request: httpx.Response(200, content=payload.encode()))
    )
    provider = OpenAICompatibleProvider(client)
    chunks = []
    async for event in provider.stream([Message(role="user", content="ping")], _config()):
        chunks.append(event)

    assert chunks[0].delta == "Hel"
    assert chunks[1].delta == "lo"
    assert chunks[2].finish is True
    assert chunks[2].input_tokens == 2
    assert chunks[2].output_tokens == 2
    await client.aclose()


async def test_anthropic_provider_maps_conversation_and_tools() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        assert request.headers["anthropic-version"] == "2023-06-01"
        assert body["system"] == "You are a test agent."
        assert body["messages"][0]["role"] == "user"
        assert body["tools"][0]["name"] == "add"
        return httpx.Response(
            200,
            json={
                "model": "claude-3",
                "content": [
                    {"type": "text", "text": "working"},
                    {"type": "tool_use", "id": "toolu_1", "name": "add", "input": {"numbers": [4]}},
                ],
                "usage": {"input_tokens": 7, "output_tokens": 2},
            },
        )

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = AnthropicProvider(client)
    completion = await provider.complete(
        [
            Message(role="system", content="You are a test agent."),
            Message(role="user", content="add"),
        ],
        _config(model="claude-3"),
        tools=[ToolSpec(name="add", description="sum", parameters={"type": "object"})],
    )

    assert completion.text == "working"
    assert completion.tool_calls[0].id == "toolu_1"
    assert completion.tool_calls[0].arguments == {"numbers": [4]}
    assert completion.input_tokens == 7
    assert completion.finish_reason == "tool_calls"
    await client.aclose()


async def test_anthropic_provider_rejects_structured_output() -> None:
    client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: httpx.Response(200, json={})))
    provider = AnthropicProvider(client)
    with pytest.raises(ProviderError, match="structured"):
        await provider.complete([Message(role="user", content="x")], _config(), response_schema={"type": "object"})
    await client.aclose()


async def test_gemini_provider_maps_contents_tools_and_usage() -> None:
    requests: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["x-goog-api-key"] == "test-key"
        assert request.url.params.get("alt") is None
        body = json.loads(request.content)
        requests.append(body)
        assert body["systemInstruction"]["parts"][0]["text"] == "You are a test agent."
        assert body["contents"][0]["role"] == "user"
        assert body["tools"][0]["functionDeclarations"][0]["name"] == "add"
        assert body["generationConfig"]["responseMimeType"] == "application/json"
        if len(requests) == 2:
            call_part = next(part for part in body["contents"][1]["parts"] if "functionCall" in part)
            assert call_part["thoughtSignature"] == "sig-123"
            return httpx.Response(
                200,
                json={
                    "candidates": [{"content": {"parts": [{"text": "final"}]}}],
                    "usageMetadata": {"promptTokenCount": 3, "candidatesTokenCount": 2},
                    "modelVersion": "gemini-2",
                },
            )
        return httpx.Response(
            200,
            json={
                "candidates": [
                    {
                        "content": {
                            "parts": [
                                {"text": "ok"},
                                {
                                    "functionCall": {"name": "add", "args": {"numbers": [5]}},
                                    "thoughtSignature": "sig-123",
                                },
                            ]
                        }
                    }
                ],
                "usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 4, "thoughtsTokenCount": 3},
                "modelVersion": "gemini-2",
            },
        )

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = GeminiProvider(client)
    messages = [
        Message(role="system", content="You are a test agent."),
        Message(role="user", content="add"),
    ]
    tools = [ToolSpec(name="add", description="sum", parameters={"type": "object"})]
    completion = await provider.complete(
        messages,
        _config(),
        tools=tools,
        response_schema={"type": "object"},
    )

    assert completion.text == "ok"
    assert completion.tool_calls[0].name == "add"
    assert completion.tool_calls[0].arguments == {"numbers": [5]}
    assert completion.tool_calls[0].signature == "sig-123"
    assert completion.input_tokens == 9
    assert completion.output_tokens == 7

    follow_up = await provider.complete(
        [
            *messages,
            Message(role="assistant", content="ok", tool_calls=completion.tool_calls),
            Message(role="tool", content='{"sum": 5}', tool_call_id=completion.tool_calls[0].id, name="add"),
        ],
        _config(),
        tools=tools,
        response_schema={"type": "object"},
    )
    assert follow_up.text == "final"
    await client.aclose()


async def test_gemini_provider_normalises_rate_limit() -> None:
    client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: httpx.Response(429, json={})))
    provider = GeminiProvider(client)
    with pytest.raises(ProviderError) as exc_info:
        await provider.complete([Message(role="user", content="ping")], _config())
    assert exc_info.value.code == "rate_limited"
    assert exc_info.value.rate_limited is True
    await client.aclose()


async def test_echo_provider_streams() -> None:
    provider = EchoProvider()
    config = ProviderConfig()
    expected = (await provider.complete([Message(role="user", content="ping")], config)).text

    chunks = []
    async for event in provider.stream([Message(role="user", content="ping")], config):
        chunks.append(event)

    assert "".join(chunk.delta for chunk in chunks if chunk.delta) == expected
    assert chunks[-1].finish is True
    assert chunks[-1].input_tokens > 0
