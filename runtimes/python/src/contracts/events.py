"""Event envelope and the Phase 1 event payloads."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

ExecutionState = Literal["pending", "running", "failing", "cancel_requested", "succeeded", "failed", "cancelled"]
TaskState = Literal["pending", "assigned", "running", "succeeded", "failed", "cancelled", "skipped"]
AttemptOutcome = Literal["succeeded", "failed", "cancelled", "lease_expired", "incomplete"]
SkipReason = Literal["branch_not_taken", "upstream_not_taken", "dependency_failed"]
NodeType = Literal["agent", "tool"]

ORG_ID_PATTERN = r"^org_[A-Za-z0-9_-]{8,64}$"
PROJECT_ID_PATTERN = r"^proj_[A-Za-z0-9_-]{8,64}$"
EXECUTION_ID_PATTERN = r"^exec_[A-Za-z0-9_-]{8,64}$"
TASK_ID_PATTERN = r"^task_[A-Za-z0-9_-]{8,64}$"
ATTEMPT_ID_PATTERN = r"^att_[A-Za-z0-9_-]{8,64}$"
WORKER_ID_PATTERN = r"^wrk_[A-Za-z0-9_-]{8,64}$"
MESSAGE_ID_PATTERN = r"^msg_[A-Za-z0-9_-]{8,64}$"
WORKFLOW_VERSION_ID_PATTERN = r"^wfv_[A-Za-z0-9_-]{8,64}$"
AGENT_VERSION_ID_PATTERN = r"^agtv_[A-Za-z0-9_-]{8,64}$"
SHA256_PATTERN = r"^sha256:[0-9a-f]{64}$"
TRACEPARENT_PATTERN = r"^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$"

EVENT_EXECUTION_CREATED = "execution.created"
EVENT_EXECUTION_STATE_CHANGED = "execution.state_changed"
EVENT_TASK_STATE_CHANGED = "task.state_changed"
EVENT_TASK_ATTEMPT_FINISHED = "task.attempt_finished"
EVENT_TASK_DISPATCH = "task.dispatch"

SUPPORTED_EVENT_TYPES: dict[str, str] = {
    EVENT_EXECUTION_CREATED: "events/execution.created.schema.json",
    EVENT_EXECUTION_STATE_CHANGED: "events/execution.state_changed.schema.json",
    EVENT_TASK_STATE_CHANGED: "events/task.state_changed.schema.json",
    EVENT_TASK_ATTEMPT_FINISHED: "events/task.attempt_finished.schema.json",
    EVENT_TASK_DISPATCH: "events/task.dispatch.schema.json",
}

SupportedEventTypes = SUPPORTED_EVENT_TYPES


def _timestamp(value: str) -> str:
    try:
        datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError("must be an RFC 3339 timestamp") from exc
    return value


class EventEnvelope(BaseModel):
    """Envelope schema is version 1. Unknown fields are ignored for forward compatibility."""

    model_config = ConfigDict(extra="allow", frozen=True)

    message_id: str = Field(pattern=MESSAGE_ID_PATTERN)
    message_type: str
    schema_version: int
    tenant_id: str | None = Field(default=None, pattern=ORG_ID_PATTERN)
    project_id: str | None = Field(default=None, pattern=PROJECT_ID_PATTERN)
    execution_id: str | None = Field(default=None, pattern=EXECUTION_ID_PATTERN)
    task_id: str | None = Field(default=None, pattern=TASK_ID_PATTERN)
    attempt_id: str | None = Field(default=None, pattern=ATTEMPT_ID_PATTERN)
    worker_id: str | None = Field(default=None, pattern=WORKER_ID_PATTERN)
    timestamp: str
    correlation_id: str | None = None
    causation_id: str | None = None
    traceparent: str | None = Field(default=None, pattern=TRACEPARENT_PATTERN)
    payload: dict[str, Any]

    @field_validator("timestamp")
    @classmethod
    def _timestamp(cls, value: str) -> str:
        return _timestamp(value)

    @model_validator(mode="after")
    def _check(self) -> Self:
        problems: list[str] = []
        if self.message_type not in SUPPORTED_EVENT_TYPES:
            problems.append(f"unsupported message_type {self.message_type!r}")
        if self.schema_version != 1:
            problems.append("schema_version must be 1")
        for field in ("tenant_id", "project_id", "execution_id"):
            if getattr(self, field) is None:
                problems.append(f"{field} is required")
        if self.message_type in (
            EVENT_TASK_STATE_CHANGED,
            EVENT_TASK_ATTEMPT_FINISHED,
            EVENT_TASK_DISPATCH,
        ):
            for field in ("task_id", "attempt_id"):
                if getattr(self, field) is None:
                    problems.append(f"{field} is required for {self.message_type}")
        if self.message_type == EVENT_TASK_DISPATCH and self.worker_id is None:
            problems.append("worker_id is required for task.dispatch")
        if problems:
            raise ValueError("; ".join(problems))
        return self


class PayloadModel(BaseModel):
    model_config = ConfigDict(extra="allow", frozen=True)


class ExecutionCounts(PayloadModel):
    total: int = Field(ge=0)
    pending: int = Field(ge=0)
    assigned: int = Field(ge=0)
    running: int = Field(ge=0)
    succeeded: int = Field(ge=0)
    failed: int = Field(ge=0)
    cancelled: int = Field(ge=0)
    skipped: int = Field(ge=0)

    @model_validator(mode="after")
    def _sum(self) -> Self:
        states = (
            self.pending + self.assigned + self.running + self.succeeded + self.failed + self.cancelled + self.skipped
        )
        if states != self.total:
            raise ValueError("counts.total must equal the sum of task states")
        return self


class ExecutionCreatedPayload(PayloadModel):
    seq: int = Field(ge=1)
    workflow_version_id: str = Field(pattern=WORKFLOW_VERSION_ID_PATTERN)
    state: ExecutionState
    state_version: int = Field(ge=1)
    counts: ExecutionCounts


class ExecutionStateChangedPayload(PayloadModel):
    seq: int = Field(ge=1)
    from_: ExecutionState = Field(alias="from")
    to: ExecutionState
    state_version: int = Field(ge=2)
    reason: str | None = Field(default=None, max_length=500)
    failure_code: str | None = Field(default=None, max_length=128)
    counts: ExecutionCounts

    model_config = ConfigDict(extra="allow", frozen=True, populate_by_name=True, serialize_by_alias=True)


class TaskStateChangedPayload(PayloadModel):
    seq: int = Field(ge=1)
    task_id: str = Field(pattern=TASK_ID_PATTERN)
    node_id: str = Field(pattern=r"^[a-z][a-z0-9_]{0,63}$")
    from_: TaskState = Field(alias="from")
    to: TaskState
    state_version: int = Field(ge=1)
    reason: str | None = Field(default=None, max_length=500)
    attempt_id: str | None = Field(default=None, pattern=ATTEMPT_ID_PATTERN)
    skip_reason: SkipReason | None = None

    model_config = ConfigDict(extra="allow", frozen=True, populate_by_name=True, serialize_by_alias=True)


class TaskAttemptFinishedPayload(PayloadModel):
    seq: int = Field(ge=1)
    attempt_id: str = Field(pattern=ATTEMPT_ID_PATTERN)
    attempt_number: int = Field(ge=1)
    worker_id: str = Field(min_length=1, max_length=128)
    outcome: AttemptOutcome
    error_code: str | None = Field(default=None, max_length=128)
    effect_uncertain: bool
    duration_ms: int | None = Field(default=None, ge=0)


class TaskDispatchPayload(PayloadModel):
    attempt_number: int = Field(ge=1)
    node_id: str = Field(pattern=r"^[a-z][a-z0-9_]{0,63}$")
    node_type: NodeType
    agent_version_id: str | None = Field(default=None, pattern=AGENT_VERSION_ID_PATTERN)
    tool_version_id: str | None = Field(default=None, min_length=1, max_length=128)
    spec_digest: str = Field(pattern=SHA256_PATTERN)
    deadline: str
    lease_expires_at: str

    @field_validator("deadline", "lease_expires_at")
    @classmethod
    def _timestamps(cls, value: str) -> str:
        return _timestamp(value)

    @model_validator(mode="after")
    def _refs(self) -> Self:
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


@dataclass(frozen=True)
class ParsedEvent:
    envelope: EventEnvelope
    payload: PayloadModel


_PAYLOAD_TYPES: dict[str, type[PayloadModel]] = {
    EVENT_EXECUTION_CREATED: ExecutionCreatedPayload,
    EVENT_EXECUTION_STATE_CHANGED: ExecutionStateChangedPayload,
    EVENT_TASK_STATE_CHANGED: TaskStateChangedPayload,
    EVENT_TASK_ATTEMPT_FINISHED: TaskAttemptFinishedPayload,
    EVENT_TASK_DISPATCH: TaskDispatchPayload,
}


def parse_event(data: bytes | str) -> ParsedEvent:
    envelope = EventEnvelope.model_validate_json(data)
    payload_type = _PAYLOAD_TYPES[envelope.message_type]
    payload = payload_type.model_validate(envelope.payload)
    return ParsedEvent(envelope=envelope, payload=payload)
