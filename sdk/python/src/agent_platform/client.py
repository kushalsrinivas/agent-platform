"""Async HTTP client."""

from __future__ import annotations

from types import TracebackType
from typing import Any, Self

import httpx

from agent_platform.models import Health, Meta


class APIError(Exception):
    """A non-2xx API response."""

    def __init__(self, status_code: int, body: str) -> None:
        super().__init__(f"api: status {status_code}: {body}")
        self.status_code = status_code
        self.body = body


class Client:
    """Calls the public API. Caller-owned retries: only idempotent calls should be retried."""

    def __init__(
        self,
        base_url: str,
        *,
        token: str | None = None,
        timeout: float = 10.0,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        headers = {"Accept": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        self._http = httpx.AsyncClient(
            base_url=base_url.rstrip("/"),
            headers=headers,
            timeout=timeout,
            transport=transport,
        )

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        await self._http.aclose()

    async def health(self) -> Health:
        return Health.model_validate(await self._get("/healthz"))

    async def ready(self) -> None:
        await self._get("/readyz")

    async def meta(self) -> Meta:
        return Meta.model_validate(await self._get("/v1/meta"))

    async def _get(self, path: str) -> dict[str, Any]:
        response = await self._http.get(path)
        if response.status_code < 200 or response.status_code >= 300:
            raise APIError(response.status_code, response.text[:4096])
        data: dict[str, Any] = response.json()
        return data
