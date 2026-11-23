import httpx
import pytest

from model_providers import (
    EchoProvider,
    Message,
    OpenAICompatibleProvider,
    ProviderConfig,
    ProviderError,
)


async def test_echo_provider_is_deterministic() -> None:
    provider = EchoProvider()
    messages = [Message(role="user", content="ping")]

    first = await provider.complete(messages, ProviderConfig())
    second = await provider.complete(messages, ProviderConfig())

    assert first == second
    assert first.text.endswith("ping")
    assert first.text.startswith("[echo:")


async def test_openai_compatible_provider_parses_response() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["authorization"] == "Bearer test-key"
        return httpx.Response(
            200,
            json={
                "model": "test-model",
                "choices": [{"message": {"content": "pong"}}],
                "usage": {"prompt_tokens": 3, "completion_tokens": 1},
            },
        )

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    provider = OpenAICompatibleProvider(client)
    config = ProviderConfig(
        model="test-model",
        base_url="https://provider.invalid/v1",
        api_key="test-key",  # type: ignore[arg-type]
    )

    completion = await provider.complete([Message(role="user", content="ping")], config)

    assert completion.text == "pong"
    assert completion.usage["prompt_tokens"] == 3
    await client.aclose()


async def test_openai_compatible_provider_requires_config() -> None:
    provider = OpenAICompatibleProvider()
    with pytest.raises(ProviderError, match="base_url"):
        await provider.complete([Message(role="user", content="ping")], ProviderConfig())
