"""Runtime ABI service.

One process serves one attempt over a per-attempt Unix socket. The worker owns the lifecycle: it starts the
process, waits for the socket, calls ``ExecuteNode`` and terminates the process group on cancellation. The
runtime holds no database, NATS or object-store credentials.
"""

from __future__ import annotations

import asyncio
import logging
import time
from datetime import UTC
from typing import Any

import grpc
from google.protobuf.json_format import MessageToDict

from agent_runtime.events import EventEmitter
from agent_runtime.langgraph_runtime import LangGraphRuntime
from agent_runtime.proto_gen import runtime_pb2, runtime_pb2_grpc
from agent_runtime.runtime import AgentRuntime, EchoAgentRuntime
from agent_runtime.spec import AgentVersionSpec, NodeContext, NodeResult
from checkpointing.codec import decode
from model_providers.base import ProviderError
from tool_runtime import ToolRegistry, default_registry

log = logging.getLogger(__name__)

RUNTIME_VERSION = "0.1.0"
RUNTIME_ABI = 1


def _struct_to_dict(value: Any) -> dict[str, Any]:
    """Deep-convert a protobuf Struct; a shallow dict() leaves nested Struct values behind."""
    if value is None or not value:
        return {}
    converted: dict[str, Any] = MessageToDict(value, preserving_proto_field_name=True)
    return converted


_CLASSIFICATIONS = {
    "TASK_TIMEOUT": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_TIMEOUT,
    "CANCELLED": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_CANCELLED,
    "MAX_TURNS_EXCEEDED": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
    "SPEC_INVALID": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
    "CHECKPOINT_INVALID": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
    "RESULT_INVALID": runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
}


def _failure(result: NodeResult) -> Any:
    """Map a sanitised runtime error onto the platform classification enum."""
    error = result.error
    if error is None:
        return runtime_pb2.RuntimeFailure(code="RUNTIME_FAILED", message="runtime failed without detail")
    if error.code in _CLASSIFICATIONS:
        classification = _CLASSIFICATIONS[error.code]
    elif error.retryable:
        classification = runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_TRANSIENT
    else:
        classification = runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT
    return runtime_pb2.RuntimeFailure(
        code=error.code,
        message=error.message,
        classification=classification,
    )


class AgentRuntimeServicer(runtime_pb2_grpc.AgentRuntimeServicer):  # type: ignore[misc]
    """Executes one agent node per process."""

    def __init__(self, runtime_factory: Any | None = None, tools: ToolRegistry | None = None) -> None:
        self._runtime_factory = runtime_factory
        self._tools = tools

    async def ExecuteNode(self, request: Any, context: Any) -> Any:
        task = request.context
        started = time.monotonic()
        emitter = EventEmitter(task.execution_id, task.task_id, task.attempt_id, task.node_id)
        emitter.task_started()

        node_context = NodeContext(
            org_id=task.org_id,
            project_id=task.project_id,
            execution_id=task.execution_id,
            task_id=task.task_id,
            attempt_id=task.attempt_id,
            node_id=task.node_id,
            agent_version_id=task.agent_version_id,
            execution_input=_struct_to_dict(task.execution_input),
            node_params=_struct_to_dict(task.node_params),
            upstream_outputs={
                node: _struct_to_dict(output) for node, output in dict(task.upstream_outputs or {}).items()
            },
            deadline=task.deadline.ToDatetime(UTC) if task.HasField("deadline") else None,
            traceparent=task.traceparent or None,
        )

        try:
            spec = AgentVersionSpec.from_mapping(_struct_to_dict(task.version_spec))
        except Exception as exc:
            emitter.task_failed("SPEC_INVALID", str(exc))
            return runtime_pb2.ExecuteNodeResponse(
                outcome=runtime_pb2.RUNTIME_OUTCOME_FAILED,
                runtime_version=RUNTIME_VERSION,
                failure=runtime_pb2.RuntimeFailure(
                    code="SPEC_INVALID",
                    message="agent version spec could not be parsed",
                    classification=runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
                ),
            )

        if self._runtime_factory is not None:
            runtime: AgentRuntime = self._runtime_factory()
        else:
            runtime = LangGraphRuntime(emitter=emitter, tools=self._tools)

        try:
            await runtime.initialize(spec, node_context)
            if request.resume_checkpoint:
                checkpoint = decode(bytes(request.resume_checkpoint))
                await runtime.restore_state(checkpoint.model_dump())
            result = await runtime.execute(_struct_to_dict(task.execution_input), node_context)
            await runtime.shutdown()
        except ProviderError as exc:
            emitter.task_failed(exc.code, exc.message)
            return runtime_pb2.ExecuteNodeResponse(
                outcome=runtime_pb2.RUNTIME_OUTCOME_FAILED,
                runtime_version=RUNTIME_VERSION,
                failure=runtime_pb2.RuntimeFailure(
                    code=exc.code,
                    message=exc.message,
                    classification=runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_TRANSIENT
                    if exc.retryable
                    else runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT,
                ),
            )
        except Exception as exc:
            log.exception("runtime crashed", extra={"task_id": task.task_id})
            emitter.task_failed("RUNTIME_CRASH", str(exc))
            return runtime_pb2.ExecuteNodeResponse(
                outcome=runtime_pb2.RUNTIME_OUTCOME_FAILED,
                runtime_version=RUNTIME_VERSION,
                failure=runtime_pb2.RuntimeFailure(
                    code="RUNTIME_CRASH",
                    message="runtime process failed unexpectedly",
                    classification=runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_RUNTIME_ERROR,
                ),
            )

        if result.outcome != "succeeded":
            error = result.error
            emitter.task_failed(error.code if error else "RUNTIME_FAILED", error.message if error else "")

        response = runtime_pb2.ExecuteNodeResponse(runtime_version=RUNTIME_VERSION)
        response.metrics.duration_ms = int((time.monotonic() - started) * 1000)
        response.metrics.tokens_in = result.input_tokens
        response.metrics.tokens_out = result.output_tokens
        response.metrics.steps = result.steps
        if result.outcome == "succeeded":
            response.outcome = runtime_pb2.RUNTIME_OUTCOME_SUCCEEDED
            response.output.update(result.output)
            if result.checkpoint:
                response.checkpoint = result.checkpoint
                response.checkpoint_schema_version = result.checkpoint_schema_version
        elif result.outcome == "cancelled":
            response.outcome = runtime_pb2.RUNTIME_OUTCOME_CANCELLED
        else:
            response.outcome = runtime_pb2.RUNTIME_OUTCOME_FAILED
            response.failure.CopyFrom(_failure(result))
        return response


class ToolRuntimeServicer(runtime_pb2_grpc.ToolRuntimeServicer):  # type: ignore[misc]
    """Executes platform-provided tools for tool nodes."""

    def __init__(self, tools: ToolRegistry | None = None) -> None:
        self._tools = tools or default_registry()

    async def ExecuteTool(self, request: Any, context: Any) -> Any:
        tool = request.context
        started = time.monotonic()
        outcome = await self._tools.execute(tool.tool_id, _struct_to_dict(tool.arguments))
        response = runtime_pb2.ExecuteToolResponse(runtime_version=RUNTIME_VERSION)
        response.metrics.duration_ms = int((time.monotonic() - started) * 1000)
        if outcome.ok:
            response.outcome = runtime_pb2.RUNTIME_OUTCOME_SUCCEEDED
            response.output.update(outcome.output)
        else:
            response.outcome = runtime_pb2.RUNTIME_OUTCOME_FAILED
            response.failure.code = outcome.error_code or "TOOL_FAILED"
            response.failure.message = outcome.error_message or "tool execution failed"
            response.failure.classification = runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT
        return response


async def serve(socket_path: str, *, runtime_factory: Any | None = None, tools: ToolRegistry | None = None) -> None:
    """Serve the runtime ABI on a Unix socket until the worker terminates the process."""
    server = grpc.aio.server()
    runtime_pb2_grpc.add_AgentRuntimeServicer_to_server(AgentRuntimeServicer(runtime_factory, tools), server)
    runtime_pb2_grpc.add_ToolRuntimeServicer_to_server(ToolRuntimeServicer(tools), server)
    bound = server.add_insecure_port(f"unix:{socket_path}")
    if bound == 0:
        raise RuntimeError(f"could not bind runtime socket {socket_path}")
    await server.start()
    log.info(
        "runtime listening",
        extra={"socket": socket_path, "runtime_version": RUNTIME_VERSION, "abi": RUNTIME_ABI},
    )
    try:
        await server.wait_for_termination()
    except asyncio.CancelledError:
        await server.stop(0)
        raise


__all__ = [
    "RUNTIME_ABI",
    "RUNTIME_VERSION",
    "AgentRuntimeServicer",
    "EchoAgentRuntime",
    "ToolRuntimeServicer",
    "serve",
]
