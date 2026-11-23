"""Runtime entry point.

The protocol is deliberately small: one attempt in, one result out. Everything else (leases, retries,
checkpoint storage) belongs to the Go side.
"""

from __future__ import annotations

import logging
from typing import Protocol, runtime_checkable

from agent_runtime.spec import AgentSpec, NodeContext, NodeResult

log = logging.getLogger(__name__)


@runtime_checkable
class AgentRuntime(Protocol):
    """What the worker calls over the per-attempt Unix socket."""

    async def execute_node(
        self,
        spec: AgentSpec,
        context: NodeContext,
        *,
        resume_checkpoint: bytes | None = None,
    ) -> NodeResult: ...


class EchoAgentRuntime:
    """Deterministic runtime used by tests and the local smoke path.

    It proves the attempt plumbing end to end without calling a model provider.
    """

    async def execute_node(
        self,
        spec: AgentSpec,
        context: NodeContext,
        *,
        resume_checkpoint: bytes | None = None,
    ) -> NodeResult:
        text = str(context.node_params.get("input", context.execution_input.get("input", "")))
        log.info(
            "echo node executed",
            extra={
                "execution_id": context.execution_id,
                "task_id": context.task_id,
                "attempt_id": context.attempt_id,
                "node_id": context.node_id,
                "resumed": resume_checkpoint is not None,
            },
        )
        return NodeResult.success({"echo": text, "agent": spec.name, "steps": 1})
