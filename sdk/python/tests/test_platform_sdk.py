"""SDK contract tests against an in-process HTTP transport."""

from __future__ import annotations

import json

import httpx
import pytest

from platform_sdk import AsyncClient, Client, ConflictError, NotFoundError


def test_agents_and_workflows_use_api_key_and_idempotency() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        path = request.url.path
        if path.endswith("/agents") and request.method == "POST":
            body = json.loads(request.content)
            return httpx.Response(
                201,
                json={
                    "id": "agt_1",
                    "org_id": "org_1",
                    "project_id": "proj_1",
                    "name": body["name"],
                    "latest_version": 1,
                    "created_at": "2026-01-01T00:00:00Z",
                    "updated_at": "2026-01-01T00:00:00Z",
                },
            )
        if path.endswith("/versions/1:publish"):
            return httpx.Response(
                200,
                json={
                    "id": "agtv_1",
                    "agent_id": "agt_1",
                    "version": 1,
                    "status": "published",
                    "digest": "sha256:abc",
                    "spec": {},
                    "runtime_abi": 1,
                    "created_by": "u",
                    "created_at": "2026-01-01T00:00:00Z",
                    "published_at": "2026-01-01T00:00:00Z",
                },
            )
        if path.endswith("/workflows:validate"):
            return httpx.Response(200, json={"valid": True, "nodes": 1, "edges": 0})
        if path.endswith("/workflows"):
            return httpx.Response(
                201,
                json={
                    "id": "wf_1",
                    "org_id": "org_1",
                    "project_id": "proj_1",
                    "name": "flow",
                    "latest_version": 1,
                    "created_at": "2026-01-01T00:00:00Z",
                    "updated_at": "2026-01-01T00:00:00Z",
                },
            )
        if path.endswith("/executions"):
            assert request.headers["idempotency-key"]
            return httpx.Response(
                202,
                json={
                    "id": "exec_1",
                    "org_id": "org_1",
                    "project_id": "proj_1",
                    "workflow_id": "wf_1",
                    "workflow_version_id": "wfv_1",
                    "workflow_version": 1,
                    "state": "pending",
                    "state_version": 1,
                    "priority": 0,
                    "counts": {"total": 1, "pending": 1},
                    "input_digest": "sha256:abc",
                    "created_by": "u",
                    "created_at": "2026-01-01T00:00:00Z",
                },
            )
        return httpx.Response(404, json={"error": {"code": "not_found", "message": "missing"}})

    transport = httpx.MockTransport(handler)
    with Client(
        "http://test", api_key="apk_test", org_id="org_1", project_id="proj_1", transport=transport, max_retries=0
    ) as client:
        agent = client.agents.create("research", {"model": {"provider": "echo"}})
        assert agent.id == "agt_1"
        deployed = client.agents.deploy(agent.id, 1)
        assert deployed.status == "published"
        assert client.workflows.validate({"nodes": [], "edges": []}).valid is True
        workflow = client.workflows.create("flow", {"nodes": [], "edges": []})
        execution = client.workflows.execute(workflow.id, 1, {"topic": "x"})
        assert execution.id == "exec_1"

    assert requests[0].headers["x-api-key"] == "apk_test"
    assert requests[0].url.path == "/api/v1/organizations/org_1/projects/proj_1/agents"


def test_error_mapping_and_pagination() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/agents"):
            if request.url.params.get("cursor") == "next":
                return httpx.Response(
                    200,
                    json={
                        "items": [],
                        "next_cursor": None,
                    },
                )
            return httpx.Response(
                200,
                json={
                    "items": [
                        {
                            "id": "agt_1",
                            "org_id": "org_1",
                            "project_id": "proj_1",
                            "name": "a",
                            "latest_version": 1,
                            "created_at": "2026-01-01T00:00:00Z",
                            "updated_at": "2026-01-01T00:00:00Z",
                        }
                    ],
                    "next_cursor": "next",
                },
            )
        if request.url.path.endswith("/agents/agt_missing"):
            return httpx.Response(404, json={"error": {"code": "not_found", "message": "missing"}})
        return httpx.Response(409, json={"error": {"code": "conflict", "message": "conflict"}})

    with Client(
        "http://test",
        api_key="apk_test",
        org_id="org_1",
        project_id="proj_1",
        transport=httpx.MockTransport(handler),
        max_retries=0,
    ) as client:
        first = client.agents.list()
        assert first.next_cursor == "next"
        assert [item.id for item in first.items] == ["agt_1"]
        second = client.agents.list(cursor=first.next_cursor)
        assert second.items == []
        with pytest.raises(NotFoundError):
            client.agents.get("agt_missing")
        with pytest.raises(ConflictError):
            client.request("POST", "/api/v1/conflict", json={})


@pytest.mark.asyncio
async def test_async_client_and_events() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/stream"):
            body = (
                "id: 1\nevent: task.state_changed\n"
                'data: {"seq":1,"event_id":"ev_1","event_type":"task.state_changed",'
                '"entity_type":"task","entity_id":"task_1","actor_type":"system","actor_id":"orchestrator",'
                '"payload":{},"created_at":"2026-01-01T00:00:00Z"}\n\n'
            )
            return httpx.Response(200, text=body, headers={"content-type": "text/event-stream"})
        if request.url.path.endswith("/executions/exec_1"):
            state = request.url.params.get("call", "succeeded")
            return httpx.Response(
                200,
                json={
                    "id": "exec_1",
                    "org_id": "org_1",
                    "project_id": "proj_1",
                    "workflow_id": "wf_1",
                    "workflow_version_id": "wfv_1",
                    "workflow_version": 1,
                    "state": state,
                    "state_version": 1,
                    "priority": 0,
                    "counts": {"total": 1},
                    "input_digest": "sha256:abc",
                    "created_by": "u",
                    "created_at": "2026-01-01T00:00:00Z",
                },
            )
        return httpx.Response(200, json={"items": [], "next_cursor": None})

    async with AsyncClient(
        "http://test",
        api_key="apk_test",
        org_id="org_1",
        project_id="proj_1",
        transport=httpx.MockTransport(handler),
        max_retries=0,
    ) as client:
        execution = await client.executions.get("exec_1")
        assert execution.state == "succeeded"
        events = [event async for event in client.executions.stream_events("exec_1")]
        assert events[0].seq == 1


def test_organizations_and_projects() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/v1/organizations" and request.method == "POST":
            return httpx.Response(
                201,
                json={
                    "id": "org_1",
                    "name": "Acme",
                    "slug": "acme",
                    "created_at": "2026-01-01T00:00:00Z",
                },
            )
        if request.url.path == "/api/v1/organizations":
            return httpx.Response(
                200,
                json={
                    "items": [
                        {
                            "id": "org_1",
                            "name": "Acme",
                            "slug": "acme",
                            "created_at": "2026-01-01T00:00:00Z",
                        }
                    ]
                },
            )
        if request.url.path.endswith("/projects") and request.method == "POST":
            return httpx.Response(
                201,
                json={
                    "id": "proj_1",
                    "org_id": "org_1",
                    "name": "App",
                    "slug": "app",
                    "created_at": "2026-01-01T00:00:00Z",
                    "updated_at": "2026-01-01T00:00:00Z",
                },
            )
        return httpx.Response(404, json={"error": {"code": "not_found", "message": "missing"}})

    with Client(
        "http://test", api_key="apk_test", org_id="org_1", transport=httpx.MockTransport(handler), max_retries=0
    ) as client:
        assert client.organizations.create("Acme", "acme").id == "org_1"
        assert client.organizations.list().items[0].slug == "acme"
        project = client.projects.create("App", "app")
        assert project.id == "proj_1"
