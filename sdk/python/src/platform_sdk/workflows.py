"""Workflow resources."""

from __future__ import annotations

import builtins
from collections.abc import AsyncIterator, Iterator
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client

import uuid

from platform_sdk.models import Execution, Page, ValidationResult, Workflow, WorkflowVersion


class Workflows:
    def __init__(self, client: Client) -> None:
        self._client = client

    def create(
        self, name: str, graph: dict[str, Any], *, project_id: str | None = None, description: str = ""
    ) -> Workflow:
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows",
            json={"name": name, "description": description, "version": {"graph": graph}},
        )
        return Workflow.model_validate(payload)

    def get(self, workflow_id: str, *, project_id: str | None = None) -> Workflow:
        return Workflow.model_validate(
            self._client.request("GET", f"{self._client.project_path(project_id)}/workflows/{workflow_id}")
        )

    def list(self, *, project_id: str | None = None, limit: int = 50, cursor: str | None = None) -> Page[Workflow]:
        params: dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        return self._client.page(f"{self._client.project_path(project_id)}/workflows", Workflow, params=params)

    def iter_all(self, *, project_id: str | None = None, limit: int = 50) -> Iterator[Workflow]:
        cursor = None
        while True:
            page = self.list(project_id=project_id, limit=limit, cursor=cursor)
            yield from page.items
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def validate(self, graph: dict[str, Any], *, project_id: str | None = None) -> ValidationResult:
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows:validate",
            json={"graph": graph},
        )
        return ValidationResult.model_validate(payload)

    def create_version(
        self, workflow_id: str, graph: dict[str, Any], *, project_id: str | None = None
    ) -> WorkflowVersion:
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions",
            json={"graph": graph},
        )
        return WorkflowVersion.model_validate(payload)

    def list_versions(self, workflow_id: str, *, project_id: str | None = None) -> builtins.list[WorkflowVersion]:
        payload = self._client.request(
            "GET", f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions"
        )
        return [WorkflowVersion.model_validate(item) for item in payload.get("items", [])]

    def deploy(self, workflow_id: str, version: int, *, project_id: str | None = None) -> WorkflowVersion:
        return WorkflowVersion.model_validate(
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions/{version}:publish",
            )
        )

    def execute(
        self,
        workflow_id: str,
        version: int,
        input: dict[str, Any],
        *,
        project_id: str | None = None,
        priority: int = 0,
        deadline_at: str | None = None,
        idempotency_key: str | None = None,
    ) -> Execution:
        body: dict[str, Any] = {
            "workflow_id": workflow_id,
            "workflow_version": version,
            "input": input,
            "priority": priority,
        }
        if deadline_at:
            body["deadline_at"] = deadline_at
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/executions",
            json=body,
            idempotency_key=idempotency_key or uuid.uuid4().hex,
        )
        return Execution.model_validate(payload)


class AsyncWorkflows:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def create(
        self, name: str, graph: dict[str, Any], *, project_id: str | None = None, description: str = ""
    ) -> Workflow:
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows",
            json={"name": name, "description": description, "version": {"graph": graph}},
        )
        return Workflow.model_validate(payload)

    async def get(self, workflow_id: str, *, project_id: str | None = None) -> Workflow:
        return Workflow.model_validate(
            await self._client.request("GET", f"{self._client.project_path(project_id)}/workflows/{workflow_id}")
        )

    async def list(
        self, *, project_id: str | None = None, limit: int = 50, cursor: str | None = None
    ) -> Page[Workflow]:
        params: dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        return await self._client.page(f"{self._client.project_path(project_id)}/workflows", Workflow, params=params)

    async def iter_all(self, *, project_id: str | None = None, limit: int = 50) -> AsyncIterator[Workflow]:
        cursor = None
        while True:
            page = await self.list(project_id=project_id, limit=limit, cursor=cursor)
            for item in page.items:
                yield item
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    async def validate(self, graph: dict[str, Any], *, project_id: str | None = None) -> ValidationResult:
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows:validate",
            json={"graph": graph},
        )
        return ValidationResult.model_validate(payload)

    async def create_version(
        self, workflow_id: str, graph: dict[str, Any], *, project_id: str | None = None
    ) -> WorkflowVersion:
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions",
            json={"graph": graph},
        )
        return WorkflowVersion.model_validate(payload)

    async def list_versions(self, workflow_id: str, *, project_id: str | None = None) -> builtins.list[WorkflowVersion]:
        payload = await self._client.request(
            "GET", f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions"
        )
        return [WorkflowVersion.model_validate(item) for item in payload.get("items", [])]

    async def deploy(self, workflow_id: str, version: int, *, project_id: str | None = None) -> WorkflowVersion:
        return WorkflowVersion.model_validate(
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/workflows/{workflow_id}/versions/{version}:publish",
            )
        )

    async def execute(
        self,
        workflow_id: str,
        version: int,
        input: dict[str, Any],
        *,
        project_id: str | None = None,
        priority: int = 0,
        deadline_at: str | None = None,
        idempotency_key: str | None = None,
    ) -> Execution:
        body: dict[str, Any] = {
            "workflow_id": workflow_id,
            "workflow_version": version,
            "input": input,
            "priority": priority,
        }
        if deadline_at:
            body["deadline_at"] = deadline_at
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/executions",
            json=body,
            idempotency_key=idempotency_key or uuid.uuid4().hex,
        )
        return Execution.model_validate(payload)
