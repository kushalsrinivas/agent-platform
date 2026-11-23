"""Workflow document and graph contracts."""

from __future__ import annotations

from typing import Annotated, Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, model_validator

from contracts.canonical import canonicalize, digest, graph_payload

NodeType = Literal["agent", "tool"]
SideEffectClass = Literal["read_only", "idempotent", "non_idempotent"]
CheckpointMode = Literal["none", "on_node_end", "inherit"]
RetryOn = Literal["transient", "timeout", "worker_lost", "cancelled", "any"]

NODE_ID_PATTERN = r"^[a-z][a-z0-9_]{0,63}$"
AGENT_ID_PATTERN = r"^agt_[A-Za-z0-9_-]{8,64}$"
WORKFLOW_ID_PATTERN = r"^wf_[A-Za-z0-9_-]{8,64}$"


class ContractModel(BaseModel):
    model_config = ConfigDict(
        extra="forbid",
        frozen=True,
        serialize_by_alias=True,
    )


NodeID = Annotated[str, Field(pattern=NODE_ID_PATTERN)]


class AgentRef(ContractModel):
    agent_id: str = Field(pattern=AGENT_ID_PATTERN)
    version: int = Field(ge=1)


class ToolRef(ContractModel):
    tool_id: str = Field(min_length=1, max_length=128)
    version: int = Field(ge=1)


class RetryPolicy(ContractModel):
    max_attempts: int | None = Field(default=None, ge=1, le=10)
    retry_on: list[RetryOn] | None = None
    backoff_base_ms: int | None = Field(default=None, ge=100, le=60000)
    backoff_max_ms: int | None = Field(default=None, ge=100, le=600000)

    @model_validator(mode="after")
    def _check(self) -> Self:
        problems: list[str] = []
        if self.retry_on is not None and len(set(self.retry_on)) != len(self.retry_on):
            problems.append("retry.retry_on contains duplicates")
        if (
            self.backoff_base_ms is not None
            and self.backoff_max_ms is not None
            and self.backoff_base_ms > self.backoff_max_ms
        ):
            problems.append("retry.backoff_base_ms cannot exceed backoff_max_ms")
        if problems:
            raise ValueError("; ".join(problems))
        return self


class WorkflowPolicy(ContractModel):
    on_failure: Literal["fail_fast", "continue"] | None = None


class WorkflowNode(ContractModel):
    id: str = Field(pattern=NODE_ID_PATTERN)
    type: NodeType
    agent: AgentRef | None = None
    tool: ToolRef | None = None
    params: dict[str, Any] | None = None
    input_from: list[NodeID] | None = Field(default=None, max_length=16)
    worker_pool: str | None = Field(default=None, min_length=1, max_length=64)
    timeout_seconds: int | None = Field(default=None, ge=1, le=86400)
    retry: RetryPolicy | None = None
    side_effect_class: SideEffectClass | None = None
    idempotency_key: str | None = Field(default=None, min_length=1, max_length=500)
    checkpoint_mode: CheckpointMode | None = None
    require_checkpoint: bool | None = None
    output_schema: dict[str, Any] | None = None

    @model_validator(mode="after")
    def _check(self) -> Self:
        problems: list[str] = []
        if self.type == "agent":
            if self.agent is None:
                problems.append("agent is required when type=agent")
            if self.tool is not None:
                problems.append("tool is not allowed when type=agent")
        if self.type == "tool":
            if self.tool is None:
                problems.append("tool is required when type=tool")
            if self.agent is not None:
                problems.append("agent is not allowed when type=tool")
        if self.input_from is not None:
            for ref in self.input_from:
                if ref == self.id:
                    problems.append(f"node {self.id!r} cannot read its own output")
        if self.require_checkpoint and self.checkpoint_mode == "none":
            problems.append("require_checkpoint cannot be true when checkpoint_mode=none")
        max_attempts = self.retry.max_attempts if self.retry and self.retry.max_attempts else 1
        side_effect = self.side_effect_class or "read_only"
        if side_effect == "non_idempotent" and max_attempts > 1 and self.idempotency_key is None:
            problems.append("non_idempotent nodes require idempotency_key when max_attempts > 1")
        if problems:
            raise ValueError("; ".join(problems))
        return self


class WorkflowEdge(ContractModel):
    from_: NodeID = Field(alias="from")
    to: NodeID
    when: str | None = Field(default=None, max_length=2000)

    model_config = ConfigDict(
        extra="forbid",
        frozen=True,
        populate_by_name=True,
        serialize_by_alias=True,
    )


class WorkflowGraph(ContractModel):
    nodes: list[WorkflowNode] = Field(min_length=1, max_length=500)
    edges: list[WorkflowEdge] = Field(max_length=2000)
    policy: WorkflowPolicy | None = None
    input_schema: dict[str, Any] | None = None

    @model_validator(mode="after")
    def _graph_rules(self) -> Self:
        problems: list[str] = []
        ids = [node.id for node in self.nodes]
        if len(set(ids)) != len(ids):
            duplicates = sorted({node_id for node_id in ids if ids.count(node_id) > 1})
            problems.append(f"duplicate node ids: {duplicates}")

        node_map = {node.id: node for node in self.nodes}
        adjacency: dict[str, list[str]] = {node_id: [] for node_id in ids}
        reverse: dict[str, list[str]] = {node_id: [] for node_id in ids}
        indegree = {node_id: 0 for node_id in ids}
        seen_edges: set[tuple[str, str]] = set()

        for edge in self.edges:
            if edge.from_ == edge.to:
                problems.append(f"self edge {edge.from_!r}")
            if edge.from_ not in node_map:
                problems.append(f"edge references unknown source node {edge.from_!r}")
            if edge.to not in node_map:
                problems.append(f"edge references unknown target node {edge.to!r}")
            pair = (edge.from_, edge.to)
            if pair in seen_edges:
                problems.append(f"duplicate edge {edge.from_!r} -> {edge.to!r}")
            seen_edges.add(pair)
            if edge.from_ in adjacency and edge.to in indegree:
                adjacency[edge.from_].append(edge.to)
                reverse[edge.to].append(edge.from_)
                indegree[edge.to] += 1

        queue = [node_id for node_id, degree in indegree.items() if degree == 0]
        visited = 0
        while queue:
            current = queue.pop(0)
            visited += 1
            for target in adjacency.get(current, []):
                indegree[target] -= 1
                if indegree[target] == 0:
                    queue.append(target)
        if visited != len(self.nodes):
            problems.append("workflow graph contains a cycle")

        for node in self.nodes:
            if node.input_from is None:
                continue
            ancestors = _ancestors(node.id, reverse)
            for ref in node.input_from:
                if ref not in node_map:
                    problems.append(f"node {node.id!r} input_from references unknown node {ref!r}")
                elif ref not in ancestors:
                    problems.append(f"node {node.id!r} input_from references non-upstream node {ref!r}")
            if len(set(node.input_from)) != len(node.input_from):
                problems.append(f"node {node.id!r} has duplicate input_from references")

        if problems:
            raise ValueError("; ".join(problems))
        return self

    def canonical_json(self) -> bytes:
        return canonicalize(self.model_dump(mode="json", exclude_none=True))

    def canonical_digest(self) -> str:
        return digest(self)


def _ancestors(node_id: str, reverse: dict[str, list[str]]) -> set[str]:
    seen: set[str] = set()
    queue = list(reverse.get(node_id, []))
    while queue:
        current = queue.pop()
        if current in seen:
            continue
        seen.add(current)
        queue.extend(reverse.get(current, []))
    return seen


class WorkflowDocument(WorkflowGraph):
    schema_version: Literal[1]
    workflow_id: str = Field(pattern=WORKFLOW_ID_PATTERN)
    name: str = Field(min_length=1, max_length=120)
    description: str | None = Field(default=None, max_length=2000)

    def graph_json(self) -> bytes:
        return canonicalize(graph_payload(self))

    def graph_digest(self) -> str:
        return digest(graph_payload(self))

    def document_json(self) -> bytes:
        return canonicalize(self)

    def document_digest(self) -> str:
        return digest(self)


def parse_workflow(data: bytes | str) -> WorkflowDocument:
    """Strictly decode and validate a workflow document."""
    return WorkflowDocument.model_validate_json(data)
