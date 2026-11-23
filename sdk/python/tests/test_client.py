import httpx
import pytest

from agent_platform import APIError, Client


async def test_meta_and_health() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["accept"] == "application/json"
        if request.url.path == "/v1/meta":
            return httpx.Response(
                200,
                json={
                    "name": "agent-platform-api",
                    "environment": "test",
                    "version": "dev",
                    "commit": "abc",
                    "build_date": "now",
                    "time": "2026-01-01T00:00:00Z",
                },
            )
        return httpx.Response(200, json={"status": "ok"})

    async with Client("http://test", transport=httpx.MockTransport(handler)) as api:
        meta = await api.meta()
        health = await api.health()

    assert meta.version == "dev"
    assert health.status == "ok"


async def test_ready_raises_api_error() -> None:
    transport = httpx.MockTransport(lambda _: httpx.Response(503, text="unavailable"))
    async with Client("http://test", transport=transport) as api:
        with pytest.raises(APIError) as err:
            await api.ready()

    assert err.value.status_code == 503
