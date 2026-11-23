"""Model provider abstraction.

A provider turns messages into text. Agent loops depend on this protocol, never on a vendor SDK, so a model can
be swapped per agent version without touching the runtime. Capabilities are declared, not assumed: streaming,
tool calls and structured outputs degrade gracefully when a provider does not support them.
"""

from model_providers.anthropic import AnthropicProvider
from model_providers.base import (
    Completion,
    Message,
    ModelProvider,
    ProviderCapabilities,
    ProviderConfig,
    ProviderError,
    Role,
    StreamEvent,
    ToolCall,
    ToolSpec,
)
from model_providers.echo import EchoProvider
from model_providers.gemini import GeminiProvider
from model_providers.openai_compat import OpenAICompatibleProvider
from model_providers.registry import PROVIDERS, get_provider

__all__ = [
    "PROVIDERS",
    "AnthropicProvider",
    "Completion",
    "EchoProvider",
    "GeminiProvider",
    "Message",
    "ModelProvider",
    "OpenAICompatibleProvider",
    "ProviderCapabilities",
    "ProviderConfig",
    "ProviderError",
    "Role",
    "StreamEvent",
    "ToolCall",
    "ToolSpec",
    "get_provider",
]
