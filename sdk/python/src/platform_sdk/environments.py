"""Project environment resources."""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import Environment, Page


class Environments:
    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self, *, project_id: str | None = None) -> Page[Environment]:
        return self._client.page(f"{self._client.project_path(project_id)}/environments", Environment)

    def get(self, environment_id: str, *, project_id: str | None = None) -> Environment:
        return Environment.model_validate(
            self._client.request("GET", f"{self._client.project_path(project_id)}/environments/{environment_id}")
        )

    def create(
        self, name: str, *, slug: str | None = None, kind: str = "development", project_id: str | None = None
    ) -> Environment:
        body = {"name": name, "kind": kind}
        if slug:
            body["slug"] = slug
        return Environment.model_validate(
            self._client.request("POST", f"{self._client.project_path(project_id)}/environments", json=body)
        )


class AsyncEnvironments:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def list(self, *, project_id: str | None = None) -> Page[Environment]:
        return await self._client.page(f"{self._client.project_path(project_id)}/environments", Environment)

    async def get(self, environment_id: str, *, project_id: str | None = None) -> Environment:
        return Environment.model_validate(
            await self._client.request("GET", f"{self._client.project_path(project_id)}/environments/{environment_id}")
        )

    async def create(
        self, name: str, *, slug: str | None = None, kind: str = "development", project_id: str | None = None
    ) -> Environment:
        body = {"name": name, "kind": kind}
        if slug:
            body["slug"] = slug
        return Environment.model_validate(
            await self._client.request("POST", f"{self._client.project_path(project_id)}/environments", json=body)
        )
