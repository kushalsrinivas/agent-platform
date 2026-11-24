"""Usage, workers and MCP read resources."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, cast

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import Page, Usage, Worker


class UsageResource:
    def __init__(self, client: Client) -> None:
        self._client = client

    def get(self, *, project_id: str | None = None, from_: str | None = None, to: str | None = None) -> Usage:
        params: dict[str, Any] = {}
        if project_id or self._client.project_id:
            params["project_id"] = project_id or self._client.project_id
        if from_:
            params["from"] = from_
        if to:
            params["to"] = to
        return Usage.model_validate(self._client.request("GET", f"{self._client.org_path()}/usage", params=params))

    def workers(self, *, pool: str | None = None, status: str | None = None) -> Page[Worker]:
        params = {key: value for key, value in {"pool": pool, "status": status}.items() if value}
        return self._client.page(f"{self._client.org_path()}/workers", Worker, params=params)

    def mcp_servers(self, *, project_id: str | None = None) -> dict[str, Any]:
        return cast(dict[str, Any], self._client.request("GET", f"{self._client.project_path(project_id)}/mcp/servers"))


class AsyncUsageResource:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def get(self, *, project_id: str | None = None, from_: str | None = None, to: str | None = None) -> Usage:
        params: dict[str, Any] = {}
        if project_id or self._client.project_id:
            params["project_id"] = project_id or self._client.project_id
        if from_:
            params["from"] = from_
        if to:
            params["to"] = to
        return Usage.model_validate(
            await self._client.request("GET", f"{self._client.org_path()}/usage", params=params)
        )

    async def workers(self, *, pool: str | None = None, status: str | None = None) -> Page[Worker]:
        params = {key: value for key, value in {"pool": pool, "status": status}.items() if value}
        return await self._client.page(f"{self._client.org_path()}/workers", Worker, params=params)

    async def mcp_servers(self, *, project_id: str | None = None) -> dict[str, Any]:
        return cast(
            dict[str, Any], await self._client.request("GET", f"{self._client.project_path(project_id)}/mcp/servers")
        )
