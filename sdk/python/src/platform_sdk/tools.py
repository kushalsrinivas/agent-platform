"""Tool catalog resource."""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import Page, Tool


class Tools:
    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self, *, project_id: str | None = None) -> Page[Tool]:
        return self._client.page(f"{self._client.project_path(project_id)}/tools", Tool)


class AsyncTools:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def list(self, *, project_id: str | None = None) -> Page[Tool]:
        return await self._client.page(f"{self._client.project_path(project_id)}/tools", Tool)
