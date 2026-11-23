"""Runtime ABI service tests over a real Unix socket."""

from __future__ import annotations

import asyncio
import os
import tempfile
from typing import Any

import grpc
import pytest

from agent_runtime.grpc_service import serve
from agent_runtime.proto_gen import runtime_pb2, runtime_pb2_grpc


def _spec(*, tools: bool = False) -> dict[str, Any]:
    data: dict[str, Any] = {
        "model": {"provider": "echo", "name": "echo-1", "credential_ref": "default"},
        "instructions": "You are a test agent.",
        "runtime": {"abi_version": 1, "image_digest": "sha256:" + "a" * 64},
        "limits": {"max_turns": 4, "timeout_seconds": 60},
    }
    if tools:
        data["tools"] = [{"tool_id": "add", "version": 1}]
    return data


def _request(spec: dict[str, Any], *, topic: str = "hello", resume: bytes = b"") -> Any:
    return runtime_pb2.ExecuteNodeRequest(
        runtime_abi=1,
        resume_checkpoint=resume,
        context=runtime_pb2.AgentTaskContext(
            org_id="org_1",
            project_id="proj_1",
            execution_id="exec_1",
            task_id="task_1",
            attempt_id="att_1",
            node_id="plan",
            agent_version_id="agtv_1",
            version_spec=spec,
            execution_input={"topic": topic},
        ),
    )


async def _serve() -> tuple[str, asyncio.Task[None]]:
    workdir = await asyncio.to_thread(tempfile.mkdtemp)
    socket_path = os.path.join(workdir, "rt.sock")
    server = asyncio.create_task(serve(socket_path))
    for _ in range(200):
        if await asyncio.to_thread(os.path.exists, socket_path):
            break
        await asyncio.sleep(0.05)
    return socket_path, server


async def test_execute_node_returns_validated_result_over_uds() -> None:
    socket_path, server = await _serve()
    channel = grpc.aio.insecure_channel(f"unix:{socket_path}")
    stub = runtime_pb2_grpc.AgentRuntimeStub(channel)
    try:
        response = await stub.ExecuteNode(_request(_spec()), timeout=30)
    finally:
        await channel.close()
        server.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server

    assert response.outcome == runtime_pb2.RUNTIME_OUTCOME_SUCCEEDED
    assert response.output["report"].startswith("[echo:")
    assert response.output["steps"] == 1
    assert response.metrics.tokens_in > 0
    assert response.runtime_version == "0.1.0"
    assert len(response.checkpoint) > 0


async def test_execute_node_runs_the_tool_loop_over_uds() -> None:
    socket_path, server = await _serve()
    channel = grpc.aio.insecure_channel(f"unix:{socket_path}")
    stub = runtime_pb2_grpc.AgentRuntimeStub(channel)
    try:
        response = await stub.ExecuteNode(_request(_spec(tools=True), topic="tool:add:[10,20]"), timeout=30)
    finally:
        await channel.close()
        server.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server

    assert response.outcome == runtime_pb2.RUNTIME_OUTCOME_SUCCEEDED
    assert response.output["steps"] == 2
    assert response.metrics.steps == 2


async def test_execute_node_maps_unknown_provider_to_permanent_failure() -> None:
    spec = _spec()
    spec["model"] = {"provider": "no_such_provider", "name": "x", "credential_ref": "default"}

    socket_path, server = await _serve()
    channel = grpc.aio.insecure_channel(f"unix:{socket_path}")
    stub = runtime_pb2_grpc.AgentRuntimeStub(channel)
    try:
        response = await stub.ExecuteNode(_request(spec), timeout=30)
    finally:
        await channel.close()
        server.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server

    assert response.outcome == runtime_pb2.RUNTIME_OUTCOME_FAILED
    assert response.failure.code == "unknown_provider"


async def test_execute_node_rejects_a_malformed_spec() -> None:
    spec = {"model": {"provider": 12345, "name": "x"}}

    socket_path, server = await _serve()
    channel = grpc.aio.insecure_channel(f"unix:{socket_path}")
    stub = runtime_pb2_grpc.AgentRuntimeStub(channel)
    try:
        response = await stub.ExecuteNode(_request(spec), timeout=30)
    finally:
        await channel.close()
        server.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server

    assert response.outcome == runtime_pb2.RUNTIME_OUTCOME_FAILED
    assert response.failure.code == "SPEC_INVALID"
    assert response.failure.classification == runtime_pb2.RUNTIME_FAILURE_CLASSIFICATION_PERMANENT


async def test_execute_tool_serves_the_tool_runtime() -> None:
    socket_path, server = await _serve()
    channel = grpc.aio.insecure_channel(f"unix:{socket_path}")
    stub = runtime_pb2_grpc.ToolRuntimeStub(channel)
    try:
        response = await stub.ExecuteTool(
            runtime_pb2.ExecuteToolRequest(
                context=runtime_pb2.ToolTaskContext(
                    org_id="org_1",
                    project_id="proj_1",
                    execution_id="exec_1",
                    task_id="task_1",
                    attempt_id="att_1",
                    node_id="collect",
                    tool_id="add",
                    tool_version=1,
                    version_spec={},
                    arguments={"numbers": [1, 2, 3]},
                )
            ),
            timeout=30,
        )
    finally:
        await channel.close()
        server.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server

    assert response.outcome == runtime_pb2.RUNTIME_OUTCOME_SUCCEEDED
    assert response.output["sum"] == 6
