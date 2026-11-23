"""Task dispatch and result contract tests."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker
from pydantic import ValidationError

from contracts import parse_task_dispatch, parse_task_result

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = ROOT / "packages" / "test-fixtures"
SCHEMAS = ROOT / "packages" / "schemas"


def fixture(rel: str) -> bytes:
    return (FIXTURES / rel).read_bytes()


def errors(schema_name: str, instance: object) -> list[str]:
    schema = json.loads((SCHEMAS / schema_name).read_text())
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    return [str(error.message) for error in validator.iter_errors(instance)]


def test_dispatch_fixture() -> None:
    data = fixture("tasks/dispatch.json")
    assert errors("task-dispatch.schema.json", json.loads(data)) == []
    dispatch = parse_task_dispatch(data)
    assert dispatch.node_type == "tool"
    assert dispatch.tool_version_id == "tool_web_search:1"


def test_result_fixtures() -> None:
    for name in ("result-success.json", "result-failure.json"):
        data = fixture("tasks/" + name)
        assert errors("task-result.schema.json", json.loads(data)) == []
        parse_task_result(data)


def test_success_requires_inline_output() -> None:
    instance = {
        "schema_version": 1,
        "task_id": "task_01JQ9A0B1C2D3E4F5G6H7J8K",
        "attempt_id": "att_01JQ9A0B1C2D3E4F5G6H7J8K",
        "lease_id": "8e4f3a2b-1c9d-4e8f-a0b1-c2d3e4f5a6b7",
        "outcome": "succeeded",
    }
    assert errors("task-result.schema.json", instance) != []
    with pytest.raises(ValidationError):
        parse_task_result(json.dumps(instance))


def test_agent_dispatch_rejects_tool_version() -> None:
    instance = json.loads(fixture("tasks/dispatch.json"))
    instance["node_type"] = "agent"
    instance["agent_version_id"] = "agtv_01JQ9A0B1C2D3E4F5G6H7J8K"
    with pytest.raises(ValidationError):
        parse_task_dispatch(json.dumps(instance))
