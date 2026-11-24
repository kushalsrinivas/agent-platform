"""API key resources."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from platform_sdk.client import AsyncClient, Client


from platform_sdk.models import APIKey, Page


class APIKeys:
    def __init__(self, client: Client) -> None:
        self._client = client

    def create(
        self, name: str, *, project_id: str | None = None, role: str = "operator", expires_at: str | None = None
    ) -> APIKey:
        body: dict[str, Any] = {"name": name, "role": role}
        if project_id:
            body["project_id"] = project_id
        if expires_at:
            body["expires_at"] = expires_at
        return APIKey.model_validate(self._client.request("POST", f"{self._client.org_path()}/api-keys", json=body))

    def list(self) -> Page[APIKey]:
        return self._client.page(f"{self._client.org_path()}/api-keys", APIKey)

    def revoke(self, key_id: str) -> APIKey:
        return APIKey.model_validate(self._client.request("DELETE", f"{self._client.org_path()}/api-keys/{key_id}"))


class AsyncAPIKeys:
    def __init__(self, client: AsyncClient) -> None:
        self._client = client

    async def create(
        self, name: str, *, project_id: str | None = None, role: str = "operator", expires_at: str | None = None
    ) -> APIKey:
        body: dict[str, Any] = {"name": name, "role": role}
        if project_id:
            body["project_id"] = project_id
        if expires_at:
            body["expires_at"] = expires_at
        return APIKey.model_validate(
            await self._client.request("POST", f"{self._client.org_path()}/api-keys", json=body)
        )

    async def list(self) -> Page[APIKey]:
        return await self._client.page(f"{self._client.org_path()}/api-keys", APIKey)

    async def revoke(self, key_id: str) -> APIKey:
        return APIKey.model_validate(
            await self._client.request("DELETE", f"{self._client.org_path()}/api-keys/{key_id}")
        )
