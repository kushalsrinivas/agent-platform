"""Versioned models shared by the runtime, its adapters and its tests.

They mirror the wire contract in ``proto/runtime/v1/runtime.proto`` and the agent version spec validated by the
control plane. They are intentionally permissive about extra fields so a newer orchestrator can add context
without breaking an older runtime image.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

CHECKPOINT_SCHEMA_VERSION = 1


class RuntimeModel(BaseModel):
    """Base model: extra fields are allowed, field aliases are forbidden, values are frozen on write."""

    model_config = ConfigDict(extra="allow", frozen=True)


class ModelConfig(RuntimeModel):
    """Pinned model binding. Secrets never live here; ``credential_ref`` names a task-scoped credential."""

    provider: str = "echo"
    name: str = "echo-1"
    credential_ref: str = "default"
    parameters: dict[str, Any] = Field(default_factory=dict)


class ToolBinding(RuntimeModel):
    """A tool pinned by id and version. The runtime resolves the implementation from its own image."""

    tool_id: str
    version: int = Field(default=1, ge=1)
    config: dict[str, Any] = Field(default_factory=dict)


class RuntimeInfo(RuntimeModel):
    abi_version: int = Field(default=1, ge=1, le=1000)
    image_digest: str | None = None


class LimitsSpec(RuntimeModel):
    max_turns: int = Field(default=4, ge=1, le=1000)
    timeout_seconds: int = Field(default=300, ge=1, le=86400)


class CheckpointSpec(RuntimeModel):
    mode: Literal["none", "on_node_end"] = "on_node_end"
    require_checkpoint: bool = False


class AgentVersionSpec(RuntimeModel):
    """Immutable agent version spec pinned by a workflow version."""

    model: ModelConfig = Field(default_factory=ModelConfig)
    instructions: str = ""
    tools: tuple[ToolBinding, ...] = ()
    runtime: RuntimeInfo = Field(default_factory=RuntimeInfo)
    limits: LimitsSpec = Field(default_factory=LimitsSpec)
    checkpoint: CheckpointSpec = Field(default_factory=CheckpointSpec)

    @classmethod
    def from_mapping(cls, data: dict[str, Any]) -> AgentVersionSpec:
        """Parse the spec exactly as the control plane stored it; unknown fields are preserved, not rejected."""
        return cls.model_validate(data)


class NodeContext(RuntimeModel):
    """Everything one attempt needs. Credentials are kept out of logs and serialised output."""

    org_id: str = ""
    project_id: str = ""
    execution_id: str
    task_id: str
    attempt_id: str
    node_id: str
    agent_version_id: str = ""
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

    outcome: Literal["succeeded", "failed", "cancelled"]
    output: dict[str, Any] = Field(default_factory=dict)
    checkpoint: bytes | None = None
    checkpoint_schema_version: int = CHECKPOINT_SCHEMA_VERSION
    error: RuntimeErrorInfo | None = None
    input_tokens: int = 0
    output_tokens: int = 0
    steps: int = 0
    model: str = ""

    @classmethod
    def success(
        cls,
        output: dict[str, Any],
        *,
        input_tokens: int = 0,
        output_tokens: int = 0,
        steps: int = 0,
        checkpoint: bytes | None = None,
        model: str = "",
    ) -> NodeResult:
        return cls(
            outcome="succeeded",
            output=output,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            steps=steps,
            checkpoint=checkpoint,
            model=model,
        )

    @classmethod
    def failure(cls, code: str, message: str, *, retryable: bool = False) -> NodeResult:
        return cls(outcome="failed", error=RuntimeErrorInfo(code=code, message=message, retryable=retryable))

    @classmethod
    def cancelled(cls, message: str = "execution cancelled") -> NodeResult:
        return cls(outcome="cancelled", error=RuntimeErrorInfo(code="CANCELLED", message=message))


def utcnow() -> datetime:
    return datetime.now(UTC)
