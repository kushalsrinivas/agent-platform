"""Agent resources."""

from __future__ import annotations

import builtins
from collections.abc import AsyncIterator, Iterator
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import Agent, AgentVersion, Page


class Agents:
    def __init__(self, client: Client) -> None:
        self._client = client

    def create(self, name: str, spec: dict[str, Any], *, project_id: str | None = None, description: str = "") -> Agent:
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/agents",
            json={"name": name, "description": description, "version": {"spec": spec}},
        )
        return Agent.model_validate(payload)

    def get(self, agent_id: str, *, project_id: str | None = None) -> Agent:
        return Agent.model_validate(
            self._client.request("GET", f"{self._client.project_path(project_id)}/agents/{agent_id}")
        )

    def list(self, *, project_id: str | None = None, limit: int = 50, cursor: str | None = None) -> Page[Agent]:
        params: dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        return self._client.page(f"{self._client.project_path(project_id)}/agents", Agent, params=params)

    def iter_all(self, *, project_id: str | None = None, limit: int = 50) -> Iterator[Agent]:
        cursor = None
        while True:
            page = self.list(project_id=project_id, limit=limit, cursor=cursor)
            yield from page.items
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def create_version(self, agent_id: str, spec: dict[str, Any], *, project_id: str | None = None) -> AgentVersion:
        payload = self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/agents/{agent_id}/versions",
            json={"spec": spec},
        )
        return AgentVersion.model_validate(payload)

    def list_versions(self, agent_id: str, *, project_id: str | None = None) -> builtins.list[AgentVersion]:
        payload = self._client.request("GET", f"{self._client.project_path(project_id)}/agents/{agent_id}/versions")
        return [AgentVersion.model_validate(item) for item in payload.get("items", [])]

    def get_version(self, agent_id: str, version: int, *, project_id: str | None = None) -> AgentVersion:
        return AgentVersion.model_validate(
            self._client.request("GET", f"{self._client.project_path(project_id)}/agents/{agent_id}/versions/{version}")
        )

    def deploy(self, agent_id: str, version: int, *, project_id: str | None = None) -> AgentVersion:
        return AgentVersion.model_validate(
            self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/agents/{agent_id}/versions/{version}:publish",
            )
        )


class AsyncAgents:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def create(
        self, name: str, spec: dict[str, Any], *, project_id: str | None = None, description: str = ""
    ) -> Agent:
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/agents",
            json={"name": name, "description": description, "version": {"spec": spec}},
        )
        return Agent.model_validate(payload)

    async def get(self, agent_id: str, *, project_id: str | None = None) -> Agent:
        return Agent.model_validate(
            await self._client.request("GET", f"{self._client.project_path(project_id)}/agents/{agent_id}")
        )

    async def list(self, *, project_id: str | None = None, limit: int = 50, cursor: str | None = None) -> Page[Agent]:
        params: dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        return await self._client.page(f"{self._client.project_path(project_id)}/agents", Agent, params=params)

    async def iter_all(self, *, project_id: str | None = None, limit: int = 50) -> AsyncIterator[Agent]:
        cursor = None
        while True:
            page = await self.list(project_id=project_id, limit=limit, cursor=cursor)
            for item in page.items:
                yield item
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    async def create_version(
        self, agent_id: str, spec: dict[str, Any], *, project_id: str | None = None
    ) -> AgentVersion:
        payload = await self._client.request(
            "POST",
            f"{self._client.project_path(project_id)}/agents/{agent_id}/versions",
            json={"spec": spec},
        )
        return AgentVersion.model_validate(payload)

    async def list_versions(self, agent_id: str, *, project_id: str | None = None) -> builtins.list[AgentVersion]:
        payload = await self._client.request(
            "GET", f"{self._client.project_path(project_id)}/agents/{agent_id}/versions"
        )
        return [AgentVersion.model_validate(item) for item in payload.get("items", [])]

    async def get_version(self, agent_id: str, version: int, *, project_id: str | None = None) -> AgentVersion:
        return AgentVersion.model_validate(
            await self._client.request(
                "GET", f"{self._client.project_path(project_id)}/agents/{agent_id}/versions/{version}"
            )
        )

    async def deploy(self, agent_id: str, version: int, *, project_id: str | None = None) -> AgentVersion:
        return AgentVersion.model_validate(
            await self._client.request(
                "POST",
                f"{self._client.project_path(project_id)}/agents/{agent_id}/versions/{version}:publish",
            )
        )
