"""Runtime observability.

Every lifecycle point from the phase contract is emitted as one structured JSON log line on stdout. The worker
owns the stream; the runtime never opens files or ships logs itself. Control-plane streaming of these events is
deferred until the runtime ABI grows a streaming RPC.
"""

from __future__ import annotations

import logging
from typing import Any

log = logging.getLogger("agent_runtime.events")


class EventEmitter:
    """Emits the platform event names with execution-scoped context on every line."""

    def __init__(self, execution_id: str, task_id: str, attempt_id: str, node_id: str) -> None:
        self._base: dict[str, Any] = {
            "execution_id": execution_id,
            "task_id": task_id,
            "attempt_id": attempt_id,
            "node_id": node_id,
        }

    def _emit(self, event: str, level: int = logging.INFO, **fields: Any) -> None:
        log.log(level, event, extra={**self._base, "event": event, **fields})

    def task_started(self) -> None:
        self._emit("task_started")

    def agent_initialized(self, agent_version_id: str, provider: str, model: str) -> None:
        self._emit("agent_initialized", agent_version_id=agent_version_id, provider=provider, model=model)

    def model_request_started(self, provider: str, model: str, step: int) -> None:
        self._emit("model_request_started", provider=provider, model=model, step=step)

    def model_response_received(
        self, provider: str, model: str, step: int, input_tokens: int, output_tokens: int
    ) -> None:
        self._emit(
            "model_response_received",
            provider=provider,
            model=model,
            step=step,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )

    def tool_invocation_started(self, tool: str, step: int) -> None:
        self._emit("tool_invocation_started", tool=tool, step=step)

    def tool_invocation_completed(self, tool: str, step: int, ok: bool) -> None:
        self._emit("tool_invocation_completed", tool=tool, step=step, ok=ok)

    def agent_completed(self, steps: int, input_tokens: int, output_tokens: int) -> None:
        self._emit(
            "agent_completed",
            steps=steps,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )

    def task_failed(self, code: str, detail: str) -> None:
        self._emit("task_failed", level=logging.WARNING, code=code, detail=detail)
