"""Typed response models for the Agent Platform developer API."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


class APIModel(BaseModel):
    model_config = ConfigDict(extra="allow", populate_by_name=True)


class Page[T](APIModel):
    items: list[T]
    next_cursor: str | None = None

    def __len__(self) -> int:
        return len(self.items)


class Meta(APIModel):
    name: str
    environment: str
    version: str
    commit: str
    build_date: str
    time: str


class Organization(APIModel):
    id: str
    name: str
    slug: str
    created_at: datetime


class Project(APIModel):
    id: str
    org_id: str
    name: str
    slug: str
    created_at: datetime
    updated_at: datetime


class Environment(APIModel):
    id: str
    org_id: str
    project_id: str
    name: str
    slug: str
    kind: str
    created_at: datetime
    updated_at: datetime


class Agent(APIModel):
    id: str
    org_id: str
    project_id: str
    name: str
    description: str | None = None
    latest_version: int
    created_at: datetime
    updated_at: datetime


class AgentVersion(APIModel):
    id: str
    agent_id: str
    version: int
    status: str
    digest: str
    spec: dict[str, Any]
    runtime_abi: int
    created_by: str
    created_at: datetime
    published_at: datetime | None = None


class Workflow(APIModel):
    id: str
    org_id: str
    project_id: str
    name: str
    description: str | None = None
    latest_version: int
    created_at: datetime
    updated_at: datetime


class WorkflowVersion(APIModel):
    id: str
    workflow_id: str
    version: int
    status: str
    digest: str
    graph: dict[str, Any]
    created_by: str
    created_at: datetime
    published_at: datetime | None = None


class ExecutionCounts(APIModel):
    total: int = 0
    pending: int = 0
    assigned: int = 0
    running: int = 0
    succeeded: int = 0
    failed: int = 0
    cancelled: int = 0
    skipped: int = 0


class Execution(APIModel):
    id: str
    org_id: str
    project_id: str
    workflow_id: str
    workflow_version_id: str
    workflow_version: int
    state: str
    state_version: int
    priority: int
    counts: ExecutionCounts
    input_digest: str
    created_by: str
    created_at: datetime
    started_at: datetime | None = None
    ended_at: datetime | None = None
    deadline_at: datetime | None = None
    cancel_reason: str | None = None
    failure_code: str | None = None
    failure_detail: str | None = None


class Task(APIModel):
    id: str
    execution_id: str
    node_id: str
    node_type: str
    state: str
    state_version: int
    priority: int
    attempt_count: int
    max_attempts: int
    agent_version_id: str | None = None
    tool_version_id: str | None = None
    skip_reason: str | None = None
    created_at: datetime
    started_at: datetime | None = None
    ended_at: datetime | None = None


class Attempt(APIModel):
    id: str
    task_id: str
    attempt_number: int
    worker_id: str
    outcome: str | None = None
    outcome_unknown: bool = False
    effect_uncertain: bool = False
    error_code: str | None = None
    error_message: str | None = None
    dispatched_at: datetime
    accepted_at: datetime | None = None
    started_at: datetime | None = None
    finished_at: datetime | None = None
    result_available: bool = False
    checkpoint_available: bool = False


class TaskDetail(Task):
    attempts: list[Attempt] = []
    result: dict[str, Any] | None = None


class Event(APIModel):
    seq: int
    event_id: str
    event_type: str
    entity_type: str
    entity_id: str
    actor_type: str
    actor_id: str
    from_state: str | None = None
    to_state: str | None = None
    reason: str | None = None
    payload: dict[str, Any]
    created_at: datetime


class ApprovalDecision(APIModel):
    id: str
    approval_id: str
    actor: str
    decision: str
    comment: str = ""
    created_at: datetime


class Approval(APIModel):
    id: str
    task_id: str
    prompt: str = ""
    policy: str = "any"
    approvers: list[str] = []
    status: str
    requested_at: datetime
    expires_at: datetime | None = None
    decided_at: datetime | None = None
    decided_by: str | None = None
    reason: str | None = None
    decisions: list[ApprovalDecision] = []
    project_id: str | None = None
    execution_id: str | None = None


class Delegation(APIModel):
    id: str
    root_execution_id: str
    parent_execution_id: str
    parent_task_id: str
    child_execution_id: str
    agent_id: str
    agent_version: int
    status: str
    error_code: str | None = None
    error_message: str | None = None
    created_at: datetime
    finished_at: datetime | None = None


class Tool(APIModel):
    tool_id: str
    name: str
    version: int
    description: str
    source: str
    schema_: dict[str, Any] | None = Field(default=None, alias="schema")


class Worker(APIModel):
    id: str
    pool: str
    status: str
    max_concurrency: int
    available_slots: int
    reserved_slots: int
    runtime: str
    runtime_version: str
    binary_version: str
    cpu_cores: float
    cpu_usage_percent: float | None = None
    memory_mb: int
    memory_used_mb: int | None = None
    capabilities: list[str] = []
    last_heartbeat_at: datetime


class UsageProject(APIModel):
    project_id: str
    executions: int
    succeeded: int
    failed: int
    attempts: int
    tokens_in: int
    tokens_out: int
    duration_ms: int


class Usage(APIModel):
    from_: datetime = Field(alias="from")
    to: datetime
    projects: list[UsageProject] = []
    total: UsageProject


class APIKey(APIModel):
    id: str
    org_id: str
    project_id: str | None = None
    name: str
    key_prefix: str
    role: str
    created_by: str
    created_at: datetime
    last_used_at: datetime | None = None
    expires_at: datetime | None = None
    revoked_at: datetime | None = None
    key: str | None = None


class ValidationResult(APIModel):
    valid: bool
    nodes: int
    edges: int
