"""Provider protocol and shared types."""

from __future__ import annotations

from typing import Literal, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field, SecretStr

Role = Literal["system", "user", "assistant"]


class Message(BaseModel):
    model_config = ConfigDict(frozen=True)

    role: Role
    content: str


class ProviderConfig(BaseModel):
    """Per-agent provider settings. Secrets arrive as task-scoped credentials, never from the graph."""

    model_config = ConfigDict(frozen=True)

    model: str = "echo-1"
    base_url: str | None = None
    api_key: SecretStr | None = None
    temperature: float = Field(default=0.0, ge=0.0, le=2.0)
    max_tokens: int = Field(default=1024, ge=1, le=200_000)
    timeout_seconds: float = Field(default=60.0, gt=0, le=600)


class Completion(BaseModel):
    model_config = ConfigDict(frozen=True)

    text: str
    model: str
    usage: dict[str, int] = Field(default_factory=dict)


class ProviderError(Exception):
    """Sanitised provider failure. Provider response bodies are not attached because they may echo prompts."""

    def __init__(self, code: str, message: str, *, retryable: bool = False) -> None:
        super().__init__(message)
        self.code = code
        self.retryable = retryable


@runtime_checkable
class ModelProvider(Protocol):
    async def complete(self, messages: list[Message], config: ProviderConfig) -> Completion: ...
