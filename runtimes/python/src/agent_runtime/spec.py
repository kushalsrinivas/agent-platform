"""Versioned models shared by the runtime and its tests.

These mirror the wire contract in ``proto/runtime/v1/runtime.proto``. They are intentionally permissive about
extra fields so a newer orchestrator can add context without breaking an older runtime image.
"""

from __future__ import annotations

from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field


class RuntimeModel(BaseModel):
    """Base model: extra fields are allowed, field aliases are forbidden, values are frozen on write."""

    model_config = ConfigDict(extra="allow", frozen=True)


class AgentSpec(RuntimeModel):
    """Immutable agent version spec pinned by a workflow version."""

    name: str
    runtime_abi: int = 1
    image_digest: str | None = None
    model_provider: str = "echo"
    model: str = "echo-1"
    system_prompt: str = ""
    tools: tuple[str, ...] = ()
    max_steps: int = Field(default=10, ge=1, le=100)


class NodeContext(RuntimeModel):
    """Everything one attempt needs. Credentials are deliberately kept out of logs and serialised output."""

    execution_id: str
    task_id: str
    attempt_id: str
    node_id: str
    agent_version_id: str
    execution_input: dict[str, Any] = Field(default_factory=dict)
    node_params: dict[str, Any] = Field(default_factory=dict)
    upstream_outputs: dict[str, dict[str, Any]] = Field(default_factory=dict)
    deadline: datetime | None = None
    traceparent: str | None = None
    credentials: dict[str, str] = Field(default_factory=dict, repr=False)


class RuntimeErrorInfo(RuntimeModel):
    """Sanitised failure. Never include credentials, prompts or raw provider responses."""

    code: str
    message: str
    retryable: bool = False


class NodeResult(RuntimeModel):
    """The only thing the runtime returns to the worker."""

    outcome: Literal["succeeded", "failed"]
    output: dict[str, Any] = Field(default_factory=dict)
    checkpoint: bytes | None = None
    checkpoint_schema_version: int = 1
    error: RuntimeErrorInfo | None = None
    metrics: dict[str, float] = Field(default_factory=dict)

    @classmethod
    def success(cls, output: dict[str, Any], **metrics: float) -> NodeResult:
        return cls(outcome="succeeded", output=output, metrics=metrics)

    @classmethod
    def failure(cls, code: str, message: str, *, retryable: bool = False) -> NodeResult:
        return cls(
            outcome="failed",
            error=RuntimeErrorInfo(code=code, message=message, retryable=retryable),
        )
