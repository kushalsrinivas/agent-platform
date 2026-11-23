"""Task dispatch and task result contracts."""

from __future__ import annotations

import json
import uuid
from datetime import datetime
from typing import Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from contracts.workflow import NodeID, NodeType

SHA256_PATTERN = r"^sha256:[0-9a-f]{64}$"
ORG_ID_PATTERN = r"^org_[A-Za-z0-9_-]{8,64}$"
PROJECT_ID_PATTERN = r"^proj_[A-Za-z0-9_-]{8,64}$"
EXECUTION_ID_PATTERN = r"^exec_[A-Za-z0-9_-]{8,64}$"
TASK_ID_PATTERN = r"^task_[A-Za-z0-9_-]{8,64}$"
ATTEMPT_ID_PATTERN = r"^att_[A-Za-z0-9_-]{8,64}$"
AGENT_VERSION_ID_PATTERN = r"^agtv_[A-Za-z0-9_-]{8,64}$"
UUID_PATTERN = r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"

TaskOutcome = Literal["succeeded", "failed", "cancelled"]
FailureClassification = Literal["transient", "permanent", "timeout", "cancelled", "worker_error", "result_invalid"]


class TaskModel(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


def _validate_timestamp(value: str | None) -> str | None:
    if value is None:
        return None
    try:
        datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError("must be an RFC 3339 timestamp") from exc
    return value


class TaskDispatch(TaskModel):
    schema_version: Literal[1]
    org_id: str = Field(pattern=ORG_ID_PATTERN)
    project_id: str = Field(pattern=PROJECT_ID_PATTERN)
    execution_id: str = Field(pattern=EXECUTION_ID_PATTERN)
    task_id: str = Field(pattern=TASK_ID_PATTERN)
    attempt_id: str = Field(pattern=ATTEMPT_ID_PATTERN)
    lease_id: str = Field(pattern=UUID_PATTERN)
    attempt_number: int = Field(ge=1)
    node_id: NodeID
    node_type: NodeType
    agent_version_id: str | None = Field(default=None, pattern=AGENT_VERSION_ID_PATTERN)
    tool_version_id: str | None = Field(default=None, min_length=1, max_length=128)
    spec_digest: str = Field(pattern=SHA256_PATTERN)
    deadline: str
    lease_expires_at: str

    @field_validator("deadline", "lease_expires_at")
    @classmethod
    def _timestamp(cls, value: str) -> str:
        return _validate_timestamp(value) or ""

    @field_validator("lease_id")
    @classmethod
    def _uuid(cls, value: str) -> str:
        try:
            uuid.UUID(value)
        except ValueError as exc:
            raise ValueError("must be a UUID") from exc
        return value

    @model_validator(mode="after")
    def _check(self) -> Self:
        if self.node_type == "agent":
            if self.agent_version_id is None:
                raise ValueError("agent_version_id is required for an agent task")
            if self.tool_version_id is not None:
                raise ValueError("tool_version_id is not allowed for an agent task")
        else:
            if self.tool_version_id is None:
                raise ValueError("tool_version_id is required for a tool task")
            if self.agent_version_id is not None:
                raise ValueError("agent_version_id is not allowed for a tool task")
        return self


class ArtifactRef(TaskModel):
    object_key: str = Field(min_length=1, max_length=1024)
    sha256: str = Field(pattern=SHA256_PATTERN)
    size_bytes: int = Field(ge=0)
    content_type: str | None = Field(default=None, max_length=128)


class CheckpointRef(TaskModel):
    object_key: str = Field(min_length=1, max_length=1024)
    sha256: str = Field(pattern=SHA256_PATTERN)
    size_bytes: int = Field(ge=1, le=33554432)
    checkpoint_schema_version: int = Field(ge=1)
    runtime_abi: int = Field(ge=1)


class TaskFailure(TaskModel):
    code: str = Field(min_length=1, max_length=128)
    message: str = Field(max_length=4096)
    classification: FailureClassification
    effect_uncertain: bool


class TaskMetrics(TaskModel):
    duration_ms: int | None = Field(default=None, ge=0)
    tokens_in: int | None = Field(default=None, ge=0)
    tokens_out: int | None = Field(default=None, ge=0)
    extra: dict[str, Any] | None = None


class TaskResult(TaskModel):
    schema_version: Literal[1]
    task_id: str = Field(pattern=TASK_ID_PATTERN)
    attempt_id: str = Field(pattern=ATTEMPT_ID_PATTERN)
    lease_id: str = Field(pattern=UUID_PATTERN)
    outcome: Literal["succeeded", "failed", "cancelled"]
    output: dict[str, Any] | None = None
    output_artifact: ArtifactRef | None = None
    checkpoint: CheckpointRef | None = None
    failure: TaskFailure | None = None
    metrics: TaskMetrics | None = None
    runtime_version: str | None = Field(default=None, max_length=128)
    started_at: str | None = None
    finished_at: str | None = None

    @field_validator("lease_id")
    @classmethod
    def _uuid(cls, value: str) -> str:
        try:
            uuid.UUID(value)
        except ValueError as exc:
            raise ValueError("must be a UUID") from exc
        return value

    @field_validator("started_at", "finished_at")
    @classmethod
    def _timestamp(cls, value: str | None) -> str | None:
        return _validate_timestamp(value)

    @model_validator(mode="after")
    def _check(self) -> Self:
        problems: list[str] = []
        if self.outcome == "succeeded":
            if self.output is None:
                problems.append("a successful result requires an inline output object")
            if self.failure is not None:
                problems.append("a successful result cannot carry a failure")
        if self.outcome == "failed" and self.failure is None:
            problems.append("a failed result requires a failure object")
        if self.output is not None:
            encoded = json.dumps(self.output, separators=(",", ":")).encode()
            if len(encoded) > 65536:
                problems.append("inline output exceeds 64 KiB")
        if problems:
            raise ValueError("; ".join(problems))
        return self


def parse_task_dispatch(data: bytes | str) -> TaskDispatch:
    return TaskDispatch.model_validate_json(data)


def parse_task_result(data: bytes | str) -> TaskResult:
    return TaskResult.model_validate_json(data)
