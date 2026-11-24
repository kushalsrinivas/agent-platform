"""Human approval resources."""

from __future__ import annotations

import builtins
from typing import TYPE_CHECKING, Any, cast

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import Approval, Page


class Approvals:
    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self, *, status: str | None = None, project_id: str | None = None) -> Page[Approval]:
        params: dict[str, Any] = {}
        if status:
            params["status"] = status
        if project_id or self._client.project_id:
            params["project_id"] = project_id or self._client.project_id
        return self._client.page(f"{self._client.org_path()}/approvals", Approval, params=params)

    def for_execution(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Approval]:
        payload = self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/approvals",
        )
        return [Approval.model_validate(item) for item in payload.get("items", [])]

    def approve(
        self, execution_id: str, task_id: str, *, comment: str = "", project_id: str | None = None
    ) -> dict[str, Any]:
        return self._signal(execution_id, task_id, "approve", comment, project_id)

    def reject(
        self, execution_id: str, task_id: str, *, reason: str = "", project_id: str | None = None
    ) -> dict[str, Any]:
        return self._signal(execution_id, task_id, "reject", reason, project_id)

    def signal(
        self,
        execution_id: str,
        task_id: str,
        payload: dict[str, Any],
        *,
        comment: str = "",
        project_id: str | None = None,
    ) -> dict[str, Any]:
        return cast(
            dict[str, Any],
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}:signal",
                json={"comment": comment, "payload": payload},
            ),
        )

    def _signal(
        self, execution_id: str, task_id: str, action: str, comment: str, project_id: str | None
    ) -> dict[str, Any]:
        return cast(
            dict[str, Any],
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}:{action}",
                json={"comment": comment},
            ),
        )


class AsyncApprovals:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def list(self, *, status: str | None = None, project_id: str | None = None) -> Page[Approval]:
        params: dict[str, Any] = {}
        if status:
            params["status"] = status
        if project_id or self._client.project_id:
            params["project_id"] = project_id or self._client.project_id
        return await self._client.page(f"{self._client.org_path()}/approvals", Approval, params=params)

    async def for_execution(self, execution_id: str, *, project_id: str | None = None) -> builtins.list[Approval]:
        payload = await self._client.request(
            "GET",
            f"{self._client.project_path(project_id)}/executions/{execution_id}/approvals",
        )
        return [Approval.model_validate(item) for item in payload.get("items", [])]

    async def approve(
        self, execution_id: str, task_id: str, *, comment: str = "", project_id: str | None = None
    ) -> dict[str, Any]:
        return await self._signal(execution_id, task_id, "approve", comment, project_id)

    async def reject(
        self, execution_id: str, task_id: str, *, reason: str = "", project_id: str | None = None
    ) -> dict[str, Any]:
        return await self._signal(execution_id, task_id, "reject", reason, project_id)

    async def signal(
        self,
        execution_id: str,
        task_id: str,
        payload: dict[str, Any],
        *,
        comment: str = "",
        project_id: str | None = None,
    ) -> dict[str, Any]:
        return cast(
            dict[str, Any],
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}:signal",
                json={"comment": comment, "payload": payload},
            ),
        )

    async def _signal(
        self, execution_id: str, task_id: str, action: str, comment: str, project_id: str | None
    ) -> dict[str, Any]:
        return cast(
            dict[str, Any],
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/executions/{execution_id}/tasks/{task_id}:{action}",
                json={"comment": comment},
            ),
        )
