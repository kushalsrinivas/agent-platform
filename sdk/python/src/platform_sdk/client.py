"""Synchronous and asynchronous Agent Platform clients."""

from __future__ import annotations

import asyncio
import os
import random
import time
from types import TracebackType
from typing import Any, Self, TypeVar

import httpx

from platform_sdk.exceptions import (
    APIError,
    ConfigurationError,
    PlatformTimeoutError,
    from_response,
)
from platform_sdk.models import Page

T = TypeVar("T")

DEFAULT_BASE_URL = "http://localhost:8080"
RETRY_STATUSES = {429, 502, 503, 504}


def _retry_after(response: httpx.Response) -> float | None:
    raw = response.headers.get("retry-after")
    if not raw:
        return None
    try:
        return max(0.0, float(raw))
    except ValueError:
        return None


def _error(response: httpx.Response) -> APIError:
    try:
        payload = response.json()
    except ValueError:
        payload = {}
    detail = payload.get("error", {}) if isinstance(payload, dict) else {}
    return from_response(
        response.status_code,
        str(detail.get("code", "api_error")),
        str(detail.get("message", response.text[:512])),
        details=detail.get("details"),
        request_id=detail.get("request_id"),
        retry_after=_retry_after(response),
    )


class Client:
    """Blocking client with connection pooling, safe retries and automatic API-key auth."""

    def __init__(
        self,
        base_url: str | None = None,
        *,
        api_key: str | None = None,
        token: str | None = None,
        org_id: str | None = None,
        project_id: str | None = None,
        timeout: float = 30.0,
        max_retries: int = 2,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        self.base_url = (base_url or os.environ.get("AGENT_PLATFORM_URL") or DEFAULT_BASE_URL).rstrip("/")
        self.api_key = api_key or os.environ.get("AGENT_PLATFORM_API_KEY")
        self.token = token or os.environ.get("AGENT_PLATFORM_TOKEN")
        self.org_id = org_id or os.environ.get("AGENT_PLATFORM_ORG")
        self.project_id = project_id or os.environ.get("AGENT_PLATFORM_PROJECT")
        self.max_retries = max(0, max_retries)
        headers = {"Accept": "application/json", "User-Agent": "agent-platform-python-sdk/0.2.0"}
        if self.api_key:
            headers["X-API-Key"] = self.api_key
        elif self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        self._http = httpx.Client(
            base_url=self.base_url,
            headers=headers,
            timeout=timeout,
            transport=transport,
            follow_redirects=True,
        )

        from platform_sdk.agents import Agents
        from platform_sdk.approvals import Approvals
        from platform_sdk.environments import Environments
        from platform_sdk.executions import Executions
        from platform_sdk.keys import APIKeys
        from platform_sdk.organizations import Organizations, Projects
        from platform_sdk.tools import Tools
        from platform_sdk.usage import UsageResource
        from platform_sdk.workflows import Workflows

        self.organizations = Organizations(self)
        self.projects = Projects(self)
        self.agents = Agents(self)
        self.workflows = Workflows(self)
        self.executions = Executions(self)
        self.tools = Tools(self)
        self.approvals = Approvals(self)
        self.usage = UsageResource(self)
        self.keys = APIKeys(self)
        self.environments = Environments(self)

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        self.close()

    def close(self) -> None:
        self._http.close()

    def org_path(self, org_id: str | None = None) -> str:
        org = org_id or self.org_id
        if not org:
            raise ConfigurationError("org_id is required; pass it to the client or method")
        return f"/api/v1/organizations/{org}"

    def project_path(self, project_id: str | None = None, org_id: str | None = None) -> str:
        project = project_id or self.project_id
        if not project:
            raise ConfigurationError("project_id is required; pass it to the client or method")
        return f"{self.org_path(org_id)}/projects/{project}"

    def request(
        self,
        method: str,
        path: str,
        *,
        json: dict[str, Any] | list[Any] | None = None,
        params: dict[str, Any] | None = None,
        idempotency_key: str | None = None,
        headers: dict[str, str] | None = None,
    ) -> Any:
        request_headers = dict(headers or {})
        if idempotency_key:
            request_headers["Idempotency-Key"] = idempotency_key
        retryable = method.upper() in {"GET", "HEAD"} or bool(idempotency_key)
        last_error: Exception | None = None
        for attempt in range(self.max_retries + 1):
            try:
                response = self._http.request(method, path, json=json, params=params, headers=request_headers)
            except httpx.TimeoutException as exc:
                last_error = PlatformTimeoutError(f"request timed out: {method} {path}")
                if not retryable or attempt >= self.max_retries:
                    raise last_error from exc
            except httpx.HTTPError as exc:
                last_error = APIError(0, "connection_error", str(exc))
                if not retryable or attempt >= self.max_retries:
                    raise last_error from exc
            else:
                if response.status_code in RETRY_STATUSES and retryable and attempt < self.max_retries:
                    delay = _retry_after(response)
                    time.sleep(delay if delay is not None else min(0.25 * 2**attempt, 5.0))
                    continue
                if response.status_code < 200 or response.status_code >= 300:
                    raise _error(response)
                if response.status_code == 204 or not response.content:
                    return None
                return response.json()
            time.sleep(min(0.25 * 2**attempt, 5.0) * (0.5 + random.random()))
        raise last_error or APIError(0, "unknown_error", "request failed")

    def page(
        self,
        path: str,
        model: type[T],
        *,
        params: dict[str, Any] | None = None,
        items_key: str = "items",
    ) -> Page[T]:
        payload = self.request("GET", path, params=params)
        items = payload.get(items_key, []) if isinstance(payload, dict) else []
        return Page[model](  # type: ignore[valid-type]
            items=[model.model_validate(item) for item in items],  # type: ignore[attr-defined]
            next_cursor=payload.get("next_cursor") if isinstance(payload, dict) else None,
        )


class AsyncClient:
    """Async client with the same surface as :class:`Client`."""

    def __init__(
        self,
        base_url: str | None = None,
        *,
        api_key: str | None = None,
        token: str | None = None,
        org_id: str | None = None,
        project_id: str | None = None,
        timeout: float = 30.0,
        max_retries: int = 2,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.base_url = (base_url or os.environ.get("AGENT_PLATFORM_URL") or DEFAULT_BASE_URL).rstrip("/")
        self.api_key = api_key or os.environ.get("AGENT_PLATFORM_API_KEY")
        self.token = token or os.environ.get("AGENT_PLATFORM_TOKEN")
        self.org_id = org_id or os.environ.get("AGENT_PLATFORM_ORG")
        self.project_id = project_id or os.environ.get("AGENT_PLATFORM_PROJECT")
        self.max_retries = max(0, max_retries)
        headers = {"Accept": "application/json", "User-Agent": "agent-platform-python-sdk/0.2.0"}
        if self.api_key:
            headers["X-API-Key"] = self.api_key
        elif self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        self._http = httpx.AsyncClient(
            base_url=self.base_url,
            headers=headers,
            timeout=timeout,
            transport=transport,
            follow_redirects=True,
        )

        from platform_sdk.agents import AsyncAgents
        from platform_sdk.approvals import AsyncApprovals
        from platform_sdk.environments import AsyncEnvironments
        from platform_sdk.executions import AsyncExecutions
        from platform_sdk.keys import AsyncAPIKeys
        from platform_sdk.organizations import AsyncOrganizations, AsyncProjects
        from platform_sdk.tools import AsyncTools
        from platform_sdk.usage import AsyncUsageResource
        from platform_sdk.workflows import AsyncWorkflows

        self.organizations = AsyncOrganizations(self)
        self.projects = AsyncProjects(self)
        self.agents = AsyncAgents(self)
        self.workflows = AsyncWorkflows(self)
        self.executions = AsyncExecutions(self)
        self.tools = AsyncTools(self)
        self.approvals = AsyncApprovals(self)
        self.usage = AsyncUsageResource(self)
        self.keys = AsyncAPIKeys(self)
        self.environments = AsyncEnvironments(self)

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        await self._http.aclose()

    def org_path(self, org_id: str | None = None) -> str:
        org = org_id or self.org_id
        if not org:
            raise ConfigurationError("org_id is required; pass it to the client or method")
        return f"/api/v1/organizations/{org}"

    def project_path(self, project_id: str | None = None, org_id: str | None = None) -> str:
        project = project_id or self.project_id
        if not project:
            raise ConfigurationError("project_id is required; pass it to the client or method")
        return f"{self.org_path(org_id)}/projects/{project}"

    async def request(
        self,
        method: str,
        path: str,
        *,
        json: dict[str, Any] | list[Any] | None = None,
        params: dict[str, Any] | None = None,
        idempotency_key: str | None = None,
        headers: dict[str, str] | None = None,
    ) -> Any:
        request_headers = dict(headers or {})
        if idempotency_key:
            request_headers["Idempotency-Key"] = idempotency_key
        retryable = method.upper() in {"GET", "HEAD"} or bool(idempotency_key)
        last_error: Exception | None = None
        for attempt in range(self.max_retries + 1):
            try:
                response = await self._http.request(method, path, json=json, params=params, headers=request_headers)
            except httpx.TimeoutException as exc:
                last_error = PlatformTimeoutError(f"request timed out: {method} {path}")
                if not retryable or attempt >= self.max_retries:
                    raise last_error from exc
            except httpx.HTTPError as exc:
                last_error = APIError(0, "connection_error", str(exc))
                if not retryable or attempt >= self.max_retries:
                    raise last_error from exc
            else:
                if response.status_code in RETRY_STATUSES and retryable and attempt < self.max_retries:
                    delay = _retry_after(response)
                    await asyncio.sleep(delay if delay is not None else min(0.25 * 2**attempt, 5.0))
                    continue
                if response.status_code < 200 or response.status_code >= 300:
                    raise _error(response)
                if response.status_code == 204 or not response.content:
                    return None
                return response.json()
            await asyncio.sleep(min(0.25 * 2**attempt, 5.0) * (0.5 + random.random()))
        raise last_error or APIError(0, "unknown_error", "request failed")

    async def page(
        self,
        path: str,
        model: type[T],
        *,
        params: dict[str, Any] | None = None,
        items_key: str = "items",
    ) -> Page[T]:
        payload = await self.request("GET", path, params=params)
        items = payload.get(items_key, []) if isinstance(payload, dict) else []
        return Page[model](  # type: ignore[valid-type]
            items=[model.model_validate(item) for item in items],  # type: ignore[attr-defined]
            next_cursor=payload.get("next_cursor") if isinstance(payload, dict) else None,
        )
