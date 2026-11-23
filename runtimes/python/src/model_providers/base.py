"""Provider protocol and shared types.

Providers differ; the protocol describes the common surface and each adapter declares what it supports via
``ProviderCapabilities``. Callers must degrade gracefully when a capability is absent rather than forcing every
provider into an identical shape.
"""

from __future__ import annotations

from typing import Any, Literal, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field, SecretStr

Role = Literal["system", "user", "assistant", "tool"]


class ToolCall(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    name: str
    arguments: dict[str, Any] = Field(default_factory=dict)


class Message(BaseModel):
    model_config = ConfigDict(frozen=True)

    role: Role
    content: str = ""
    name: str | None = None
    tool_call_id: str | None = None
    tool_calls: tuple[ToolCall, ...] = ()


class ToolSpec(BaseModel):
    """A tool as presented to a model: name, description and a JSON Schema for arguments."""

    model_config = ConfigDict(frozen=True)

    name: str
    description: str = ""
    parameters: dict[str, Any] = Field(default_factory=dict)


class ProviderConfig(BaseModel):
    """Per-agent provider settings. Secrets arrive as task-scoped credentials, never from the graph."""

    model_config = ConfigDict(frozen=True)

    model: str = "echo-1"
    base_url: str | None = None
    api_key: SecretStr | None = None
    temperature: float = Field(default=0.0, ge=0.0, le=2.0)
    max_tokens: int = Field(default=1024, ge=1, le=200_000)
    timeout_seconds: float = Field(default=60.0, gt=0, le=600)
    extra: dict[str, Any] = Field(default_factory=dict)


class ProviderCapabilities(BaseModel):
    model_config = ConfigDict(frozen=True)

    streaming: bool = False
    tools: bool = False
    structured: bool = False


class StreamEvent(BaseModel):
    """One streaming chunk. ``finish`` carries the terminal usage when the provider sends it."""

    model_config = ConfigDict(frozen=True)

    delta: str = ""
    input_tokens: int = 0
    output_tokens: int = 0
    finish: bool = False


class Completion(BaseModel):
    model_config = ConfigDict(frozen=True)

    text: str = ""
    model: str
    input_tokens: int = 0
    output_tokens: int = 0
    tool_calls: tuple[ToolCall, ...] = ()
    finish_reason: str | None = None
    raw_usage: dict[str, int] = Field(default_factory=dict)


class ProviderError(Exception):
    """Sanitised provider failure. Response bodies are not attached because they may echo prompts."""

    def __init__(
        self,
        code: str,
        message: str,
        *,
        retryable: bool = False,
        status_code: int | None = None,
        rate_limited: bool = False,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.retryable = retryable
        self.status_code = status_code
        self.rate_limited = rate_limited


@runtime_checkable
class ModelProvider(Protocol):
    """Non-streaming generation is mandatory; streaming, tools and structured outputs are declared capabilities."""

    name: str
    capabilities: ProviderCapabilities

    async def complete(
        self,
        messages: list[Message],
        config: ProviderConfig,
        *,
        tools: list[ToolSpec] | None = None,
        response_schema: dict[str, Any] | None = None,
    ) -> Completion: ...


def normalise_usage(data: dict[str, Any]) -> tuple[int, int]:
    """Extract input/output tokens across provider vocabularies without inventing numbers."""
    input_tokens = data.get(
        "input_tokens",
        data.get("prompt_tokens", data.get("promptTokenCount", data.get("inputTokenCount", 0))),
    )
    output_tokens = data.get(
        "output_tokens",
        data.get("completion_tokens", data.get("candidatesTokenCount", data.get("outputTokenCount", 0))),
    )
    try:
        return int(input_tokens or 0), int(output_tokens or 0)
    except (TypeError, ValueError):
        return 0, 0
