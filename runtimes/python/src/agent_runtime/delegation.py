"""Lease-scoped supervisor delegation client.

The runtime is not given database, broker or object-store credentials. Delegation is authorised by the attempt
lease the worker already holds, so the orchestrator remains the only writer of execution state.
"""

from __future__ import annotations

import asyncio
import os
from typing import Any

import grpc
from google.protobuf.struct_pb2 import Struct

from agent_runtime.proto_gen import worker_pb2, worker_pb2_grpc


class DelegationError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def _struct(value: dict[str, Any] | None) -> Struct:
    result = Struct()
    if value:
        result.update(value)
    return result


class SupervisorClient:
    """Talks to the orchestrator's worker control service with one attempt lease."""

    def __init__(self, task_id: str, attempt_id: str, lease_id: str, address: str | None = None) -> None:
        self._task_id = task_id
        self._attempt_id = attempt_id
        self._lease_id = lease_id
        self._address = (
            address
            or os.environ.get("ORCHESTRATOR_GRPC_ADDR")
            or os.environ.get("WORKER_ORCHESTRATOR_GRPC_ADDR")
            or "localhost:9090"
        )
        self._channel = grpc.aio.insecure_channel(self._address)
        self._stub = worker_pb2_grpc.WorkerControlStub(self._channel)

    def _request(self) -> dict[str, str]:
        return {"task_id": self._task_id, "attempt_id": self._attempt_id, "lease_id": self._lease_id}

    async def list_agents(self) -> dict[str, Any]:
        try:
            response = await self._stub.ListDelegationAgents(worker_pb2.ListDelegationAgentsRequest(**self._request()))
        except grpc.aio.AioRpcError as exc:
            raise DelegationError("DELEGATION_UNAVAILABLE", exc.details() or "delegation unavailable") from exc
        return {
            "agents": [
                {"agent_id": item.agent_id, "version": item.version, "name": item.name} for item in response.agents
            ],
            "max_parallel": response.max_parallel,
            "remaining_children": response.remaining_children,
        }

    async def delegate(
        self,
        agent_id: str,
        version: int,
        task: dict[str, Any],
        *,
        context: dict[str, Any] | None = None,
        context_refs: list[str] | None = None,
        output_schema: dict[str, Any] | None = None,
        timeout_seconds: int = 0,
        max_retries: int = 0,
        idempotency_key: str = "",
    ) -> dict[str, Any]:
        request = worker_pb2.DelegateTaskRequest(
            **self._request(),
            agent_id=agent_id,
            agent_version=version,
            task=_struct(task),
            context=_struct(context),
            context_refs=context_refs or [],
            timeout_seconds=timeout_seconds,
            max_retries=max_retries,
            idempotency_key=idempotency_key,
        )
        if output_schema is not None:
            request.output_schema.CopyFrom(_struct(output_schema))
        try:
            response = await self._stub.DelegateTask(request)
        except grpc.aio.AioRpcError as exc:
            raise DelegationError(exc.code().name, exc.details() or "delegation failed") from exc
        return {
            "delegation_id": response.delegation_id,
            "child_execution_id": response.child_execution_id,
            "status": response.status,
        }

    async def get(self, delegation_id: str) -> dict[str, Any]:
        try:
            response = await self._stub.GetDelegation(
                worker_pb2.GetDelegationRequest(**self._request(), delegation_id=delegation_id)
            )
        except grpc.aio.AioRpcError as exc:
            raise DelegationError(exc.code().name, exc.details() or "delegation lookup failed") from exc
        from google.protobuf.json_format import MessageToDict

        output = MessageToDict(response.output, preserving_proto_field_name=True) if response.HasField("output") else {}
        return {
            "delegation_id": response.delegation_id,
            "child_execution_id": response.child_execution_id,
            "status": response.status,
            "output": output,
            "error_code": response.error_code,
            "error_message": response.error_message,
            "tokens_in": response.tokens_in,
            "tokens_out": response.tokens_out,
        }

    async def wait(self, delegation_id: str, timeout_seconds: float) -> dict[str, Any]:
        deadline = asyncio.get_running_loop().time() + timeout_seconds
        while True:
            result = await self.get(delegation_id)
            if result["status"] in ("succeeded", "failed", "cancelled"):
                return result
            if asyncio.get_running_loop().time() >= deadline:
                result["status"] = "timeout"
                return result
            await asyncio.sleep(0.5)

    async def publish(self, topic: str, payload: dict[str, Any]) -> str:
        try:
            response = await self._stub.PublishAgentEvent(
                worker_pb2.PublishAgentEventRequest(**self._request(), topic=topic, payload=_struct(payload))
            )
        except grpc.aio.AioRpcError as exc:
            raise DelegationError(exc.code().name, exc.details() or "event publish failed") from exc
        return str(response.event_id)

    async def wait_event(self, topic: str, timeout_seconds: int = 30) -> dict[str, Any]:
        try:
            response = await self._stub.WaitAgentEvent(
                worker_pb2.WaitAgentEventRequest(**self._request(), topic=topic, timeout_seconds=timeout_seconds)
            )
        except grpc.aio.AioRpcError as exc:
            raise DelegationError(exc.code().name, exc.details() or "event wait failed") from exc
        from google.protobuf.json_format import MessageToDict

        return {
            "found": response.found,
            "event_id": response.event_id,
            "payload": MessageToDict(response.payload, preserving_proto_field_name=True)
            if response.HasField("payload")
            else {},
        }

    async def close(self) -> None:
        await self._channel.close()
