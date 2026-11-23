"""Agent runtime abstraction.

One runtime instance owns one attempt. ``initialize`` binds the pinned spec, ``execute`` runs the agent loop,
``get_state``/``restore_state`` move checkpointable state in and out, ``cancel`` requests cooperative stop and
``shutdown`` releases resources. The worker additionally enforces the hard boundary by terminating the process
group, so cancellation never depends on cooperation alone.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any

from agent_runtime.spec import AgentVersionSpec, NodeContext, NodeResult


class AgentRuntime(ABC):
    """Standardised runtime contract. Adapters (LangGraph today, others later) implement only this."""

    @abstractmethod
    async def initialize(self, spec: AgentVersionSpec, context: NodeContext) -> None:
        """Bind the immutable spec and prepare per-attempt state. Must not perform model calls."""

    @abstractmethod
    async def execute(self, input_data: dict[str, Any], context: NodeContext) -> NodeResult:
        """Run the attempt to a terminal result. Never raises for agent-level failures."""

    @abstractmethod
    async def get_state(self) -> dict[str, Any]:
        """Return checkpointable state. Must be JSON-serialisable and free of credentials."""

    @abstractmethod
    async def restore_state(self, checkpoint: dict[str, Any]) -> None:
        """Resume from a previous state. Raises ``CheckpointError`` when the state is unusable."""

    @abstractmethod
    async def cancel(self) -> None:
        """Request cooperative cancellation. The process kill remains the enforcement of last resort."""

    @abstractmethod
    async def shutdown(self) -> None:
        """Release runtime-held resources. Safe to call once per attempt."""


class EchoAgentRuntime(AgentRuntime):
    """Deterministic runtime used by tests and the offline smoke path.

    It proves the runtime contract end to end without calling a model provider.
    """

    def __init__(self) -> None:
        self._spec: AgentVersionSpec | None = None
        self._cancelled = False

    async def initialize(self, spec: AgentVersionSpec, context: NodeContext) -> None:
        self._spec = spec

    async def execute(self, input_data: dict[str, Any], context: NodeContext) -> NodeResult:
        if self._cancelled:
            return NodeResult.cancelled()
        assert self._spec is not None
        text = str(input_data.get("input", ""))
        return NodeResult.success(
            {
                "report": f"[echo] {text}",
                "steps": 1,
                "model": self._spec.model.name,
            },
            input_tokens=len(text),
            output_tokens=len(text),
            steps=1,
            model=self._spec.model.name,
        )

    async def get_state(self) -> dict[str, Any]:
        return {"done": True}

    async def restore_state(self, checkpoint: dict[str, Any]) -> None:
        # The echo runtime is stateless; a checkpoint changes nothing.
        return None

    async def cancel(self) -> None:
        self._cancelled = True

    async def shutdown(self) -> None:
        return None
