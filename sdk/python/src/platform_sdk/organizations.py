"""Organization and project resources."""

from __future__ import annotations

from typing import TYPE_CHECKING

from platform_sdk.models import Organization, Page, Project

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


class Organizations:
    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self) -> Page[Organization]:
        return self._client.page("/api/v1/organizations", Organization)

    def create(self, name: str, slug: str) -> Organization:
        return Organization.model_validate(
            self._client.request("POST", "/api/v1/organizations", json={"name": name, "slug": slug})
        )


class Projects:
    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self, *, org_id: str | None = None) -> Page[Project]:
        return self._client.page(f"{self._client.org_path(org_id)}/projects", Project)

    def get(self, project_id: str, *, org_id: str | None = None) -> Project:
        return Project.model_validate(
            self._client.request("GET", f"{self._client.org_path(org_id)}/projects/{project_id}")
        )

    def create(self, name: str, slug: str, *, org_id: str | None = None) -> Project:
        return Project.model_validate(
            self._client.request(
                "POST",
                f"{self._client.org_path(org_id)}/projects",
                json={"name": name, "slug": slug},
            )
        )


class AsyncOrganizations:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def list(self) -> Page[Organization]:
        return await self._client.page("/api/v1/organizations", Organization)

    async def create(self, name: str, slug: str) -> Organization:
        return Organization.model_validate(
            await self._client.request("POST", "/api/v1/organizations", json={"name": name, "slug": slug})
        )


class AsyncProjects:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def list(self, *, org_id: str | None = None) -> Page[Project]:
        return await self._client.page(f"{self._client.org_path(org_id)}/projects", Project)

    async def get(self, project_id: str, *, org_id: str | None = None) -> Project:
        return Project.model_validate(
            await self._client.request("GET", f"{self._client.org_path(org_id)}/projects/{project_id}")
        )

    async def create(self, name: str, slug: str, *, org_id: str | None = None) -> Project:
        return Project.model_validate(
            await self._client.request(
                "POST",
                f"{self._client.org_path(org_id)}/projects",
                json={"name": name, "slug": slug},
            )
        )
