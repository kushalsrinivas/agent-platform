"""Shared workflow, task and event contracts.

Mirrors ``packages/contracts`` in Go. The JSON Schemas in ``packages/schemas`` are the structural contract; these
models add the semantic rules that JSON Schema cannot express. Both languages are tested against the same fixtures
and canonical digests.
"""

from contracts.canonical import CanonicalError, canonicalize, digest, graph_payload, sha256_digest
from contracts.events import (
    EventEnvelope,
    ParsedEvent,
    SupportedEventTypes,
    parse_event,
)
from contracts.tasks import (
    ArtifactRef,
    CheckpointRef,
    TaskDispatch,
    TaskFailure,
    TaskMetrics,
    TaskResult,
    parse_task_dispatch,
    parse_task_result,
)
from contracts.workflow import (
    AgentRef,
    RetryPolicy,
    ToolRef,
    WorkflowDocument,
    WorkflowEdge,
    WorkflowGraph,
    WorkflowNode,
    WorkflowPolicy,
    parse_workflow,
)

__all__ = [
    "AgentRef",
    "ArtifactRef",
    "CanonicalError",
    "CheckpointRef",
    "EventEnvelope",
    "ParsedEvent",
    "RetryPolicy",
    "SupportedEventTypes",
    "TaskDispatch",
    "TaskFailure",
    "TaskMetrics",
    "TaskResult",
    "ToolRef",
    "WorkflowDocument",
    "WorkflowEdge",
    "WorkflowGraph",
    "WorkflowNode",
    "WorkflowPolicy",
    "canonicalize",
    "digest",
    "graph_payload",
    "parse_event",
    "parse_task_dispatch",
    "parse_task_result",
    "parse_workflow",
    "sha256_digest",
]
