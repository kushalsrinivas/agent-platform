"""Provider registry: spec provider name -> adapter."""

from __future__ import annotations

from collections.abc import Callable

from model_providers.anthropic import AnthropicProvider
from model_providers.base import ModelProvider, ProviderConfig, ProviderError
from model_providers.echo import EchoProvider
from model_providers.gemini import GeminiProvider
from model_providers.openai_compat import OpenAICompatibleProvider

Factory = Callable[[], ModelProvider]


def _openai() -> OpenAICompatibleProvider:
    return OpenAICompatibleProvider()


PROVIDERS: dict[str, Factory] = {
    "echo": EchoProvider,
    "openai": _openai,
    "openai_compatible": _openai,
    "openai-compatible": _openai,
    "compatible": _openai,
    "anthropic": AnthropicProvider,
    "gemini": GeminiProvider,
}


def get_provider(name: str) -> ModelProvider:
    """Resolve a provider by its spec name. Tests inject HTTP transports at the provider, not here."""
    factory = PROVIDERS.get(name.strip().lower())
    if factory is None:
        raise ProviderError("unknown_provider", f"model provider {name!r} is not configured in this runtime image")
    return factory()


__all__ = ["PROVIDERS", "ProviderConfig", "get_provider"]
