"""Execution lifecycle and event streaming."""

from __future__ import annotations

import builtins
from collections.abc import AsyncIterator, Iterator
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client

import asyncio
import json
import time
import uuid

from platform_sdk.exceptions import PlatformTimeoutError
from platform_sdk.models import Approval, Delegation, Event, Execution, Page, Task, TaskDetail

TERMINAL_STATES = {"succeeded", "failed", "cancelled"}


def _sse_event(lines: list[str]) -> Event | None:
    data = "\n".join(line[5:].strip() for line in lines if line.startswith("data:"))
    if not data:
        return None
    try:
        payload = json.loads(data)
    except json.JSONDecodeError:
        return None
    return Event.model_validate(payload)


class Executions:
    def __init__(self, client: Client) -> None:
        self._client = client

    def get(self, execution_id: str, *, project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            self._client.request("GET", f"{self._client.project_path(project_id)}/executions/{execution_id}")
        )

    def list(
        self,
        *,
        project_id: str | None = None,
        state: str | None = None,
        workflow_id: str | None = None,
        sort: str = "created_at_desc",
        created_after: str | None = None,
        created_before: str | None = None,
        limit: int = 50,
        cursor: str | None = None,
    ) -> Page[Execution]:
        params: dict[str, Any] = {"limit": limit, "sort": sort}
        for key, value in {
            "state": state,
            "workflow_id": workflow_id,
            "created_after": created_after,
            "created_before": created_before,
            "cursor": cursor,
        }.items():
            if value:
                params[key] = value
        return self._client.page(f"{self._client.project_path(project_id)}/executions", Execution, params=params)

    def iter_all(self, *, project_id: str | None = None, **kwargs: Any) -> Iterator[Execution]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = self.list(project_id=project_id, cursor=cursor, **kwargs)
            yield from page.items
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def cancel(self, execution_id: str, *, reason: str = "", project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}:cancel",
                json={"reason": reason},
            )
        )

    def resume(self, execution_id: str, *, priority: int = 0, project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}:resume",
                json={"priority": priority},
                idempotency_key=uuid.uuid4().hex,
            )
        )

    def wait(
        self, execution_id: str, *, timeout: float = 300, poll_interval: float = 0.5, project_id: str | None = None
    ) -> Execution:
        deadline = time.monotonic() + timeout
        while True:
            execution = self.get(execution_id, project_id=project_id)
            if execution.state in TERMINAL_STATES:
                return execution
            if time.monotonic() >= deadline:
                raise PlatformTimeoutError(
                    f"execution {execution_id} did not finish within {timeout}s (state={execution.state})"
                )
            time.sleep(poll_interval)

    def events(self, execution_id: str, *, after: int = 0, project_id: str | None = None) -> builtins.list[Event]:
        payload = self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/events",
            params={"after": after, "limit": 500},
        )
        return [Event.model_validate(item) for item in payload.get("items", [])]

    def stream_events(self, execution_id: str, *, after: int = 0, project_id: str | None = None) -> Iterator[Event]:
        path = f"{self._client.project_path(project_id)}/executions/{execution_id}/stream"
        with self._client._http.stream(
            "GET", path, params={"after": after}, headers={"Accept": "text/event-stream"}
        ) as response:
            if response.status_code >= 400:
                from platform_sdk.client import _error

                response.read()
                raise _error(response)
            buffer: list[str] = []
            for line in response.iter_lines():
                if line == "":
                    event = _sse_event(buffer)
                    buffer.clear()
                    if event is not None:
                        yield event
                    continue
                if line.startswith(":"):
                    continue
                buffer.append(line)
            event = _sse_event(buffer)
            if event is not None:
                yield event

    def tasks(
        self, execution_id: str, *, state: str | None = None, project_id: str | None = None
    ) -> builtins.list[Task]:
        params: dict[str, Any] | None = {"state": state} if state else None
        payload = self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks",
            params=params,
        )
        return [Task.model_validate(item) for item in payload.get("items", [])]

    def task(self, execution_id: str, task_id: str, *, project_id: str | None = None) -> TaskDetail:
        return TaskDetail.model_validate(
            self._client.request(
                "GET",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}",
            )
        )

    def approvals(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Approval]:
        payload = self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/approvals",
        )
        return [Approval.model_validate(item) for item in payload.get("items", [])]

    def delegations(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Delegation]:
        payload = self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/delegations",
        )
        return [Delegation.model_validate(item) for item in payload.get("items", [])]


class AsyncExecutions:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def get(self, execution_id: str, *, project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            await self._client.request("GET", f"{self._client.project_path(project_id)}/executions/{execution_id}")
        )

    async def list(
        self,
        *,
        project_id: str | None = None,
        state: str | None = None,
        workflow_id: str | None = None,
        sort: str = "created_at_desc",
        created_after: str | None = None,
        created_before: str | None = None,
        limit: int = 50,
        cursor: str | None = None,
    ) -> Page[Execution]:
        params: dict[str, Any] = {"limit": limit, "sort": sort}
        for key, value in {
            "state": state,
            "workflow_id": workflow_id,
            "created_after": created_after,
            "created_before": created_before,
            "cursor": cursor,
        }.items():
            if value:
                params[key] = value
        return await self._client.page(f"{self._client.project_path(project_id)}/executions", Execution, params=params)

    async def iter_all(self, *, project_id: str | None = None, **kwargs: Any) -> AsyncIterator[Execution]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = await self.list(project_id=project_id, cursor=cursor, **kwargs)
            for item in page.items:
                yield item
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    async def cancel(self, execution_id: str, *, reason: str = "", project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}:cancel",
                json={"reason": reason},
            )
        )

    async def resume(self, execution_id: str, *, priority: int = 0, project_id: str | None = None) -> Execution:
        return Execution.model_validate(
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}:resume",
                json={"priority": priority},
                idempotency_key=uuid.uuid4().hex,
            )
        )

    async def wait(
        self,
        execution_id: str,
        *,
        timeout: float = 300,  # noqa: ASYNC109 - public SDK parameter
        poll_interval: float = 0.5,
        project_id: str | None = None,
    ) -> Execution:
        deadline = time.monotonic() + timeout
        while True:
            execution = await self.get(execution_id, project_id=project_id)
            if execution.state in TERMINAL_STATES:
                return execution
            if time.monotonic() >= deadline:
                raise PlatformTimeoutError(
                    f"execution {execution_id} did not finish within {timeout}s (state={execution.state})"
                )
            await asyncio.sleep(poll_interval)

    async def events(self, execution_id: str, *, after: int = 0, project_id: str | None = None) -> builtins.list[Event]:
        payload = await self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/events",
            params={"after": after, "limit": 500},
        )
        return [Event.model_validate(item) for item in payload.get("items", [])]

    async def stream_events(
        self, execution_id: str, *, after: int = 0, project_id: str | None = None
    ) -> AsyncIterator[Event]:
        path = f"{self._client.project_path(project_id)}/executions/{execution_id}/stream"
        async with self._client._http.stream(
            "GET", path, params={"after": after}, headers={"Accept": "text/event-stream"}
        ) as response:
            if response.status_code >= 400:
                from platform_sdk.client import _error

                await response.aread()
                raise _error(response)
            buffer: list[str] = []
            async for line in response.aiter_lines():
                if line == "":
                    event = _sse_event(buffer)
                    buffer.clear()
                    if event is not None:
                        yield event
                    continue
                if line.startswith(":"):
                    continue
                buffer.append(line)
            event = _sse_event(buffer)
            if event is not None:
                yield event

    async def tasks(
        self, execution_id: str, *, state: str | None = None, project_id: str | None = None
    ) -> builtins.list[Task]:
        params: dict[str, Any] | None = {"state": state} if state else None
        payload = await self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks",
            params=params,
        )
        return [Task.model_validate(item) for item in payload.get("items", [])]

    async def task(self, execution_id: str, task_id: str, *, project_id: str | None = None) -> TaskDetail:
        return TaskDetail.model_validate(
            await self._client.request(
                "GET",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}",
            )
        )

    async def approvals(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Approval]:
        payload = await self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/approvals",
        )
        return [Approval.model_validate(item) for item in payload.get("items", [])]

    async def delegations(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Delegation]:
        payload = await self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/delegations",
        )
        return [Delegation.model_validate(item) for item in payload.get("items", [])]
