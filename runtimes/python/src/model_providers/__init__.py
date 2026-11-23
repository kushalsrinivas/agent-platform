"""Model provider abstraction.

A provider turns messages into text. Agent loops depend on this protocol, never on a vendor SDK, so a model can
be swapped per agent version without touching the runtime.
"""

from model_providers.base import (
    Completion,
    Message,
    ModelProvider,
    ProviderConfig,
    ProviderError,
    Role,
)
from model_providers.echo import EchoProvider
from model_providers.openai_compat import OpenAICompatibleProvider

__all__ = [
    "Completion",
    "EchoProvider",
    "Message",
    "ModelProvider",
    "OpenAICompatibleProvider",
    "ProviderConfig",
    "ProviderError",
    "Role",
]
