"""Workflow contract tests, mirroring the Go and JSON Schema checks."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker
from pydantic import ValidationError

from contracts import digest, parse_workflow
from contracts.workflow import WorkflowEdge

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = ROOT / "packages" / "test-fixtures"
SCHEMAS = ROOT / "packages" / "schemas"


def fixture(rel: str) -> bytes:
    return (FIXTURES / rel).read_bytes()


def schema_validator(rel: str) -> Draft202012Validator:
    schema = json.loads((SCHEMAS / rel).read_text())
    return Draft202012Validator(schema, format_checker=FormatChecker())


def schema_errors(rel: str, instance: object) -> list[str]:
    validator = schema_validator(rel)
    return [str(error.message) for error in validator.iter_errors(instance)]


def test_research_fixture_matches_schema() -> None:
    instance = json.loads(fixture("workflows/research.json"))
    assert schema_errors("workflow.schema.json", instance) == []


def test_agent_version_zero_fails_schema() -> None:
    instance = json.loads(fixture("workflows/invalid-agent-version.json"))
    assert schema_errors("workflow.schema.json", instance) != []


def test_semantic_rejections() -> None:
    cases = {
        "invalid-cycle.json": "cycle",
        "invalid-unknown-edge.json": "unknown",
        "invalid-input-from.json": "upstream",
        "invalid-side-effect.json": "idempotency_key",
    }
    for name, expected in cases.items():
        with pytest.raises(ValidationError) as err:
            parse_workflow(fixture("workflows/" + name))
        assert expected in str(err.value)


def test_document_roundtrip_and_digests_match_go() -> None:
    document = parse_workflow(fixture("workflows/research.json"))
    golden = json.loads(fixture("workflow.digests.json"))

    assert document.graph_digest() == golden["graph_sha256"]
    assert document.document_digest() == golden["document_sha256"]

    canonical = document.document_json()
    assert digest(json.loads(canonical)) == golden["document_sha256"]
    assert parse_workflow(canonical).document_digest() == golden["document_sha256"]


def test_edge_alias_serializes_as_from() -> None:
    document = parse_workflow(fixture("workflows/research.json"))
    serialized = json.loads(document.document_json())
    assert serialized["edges"][0]["from"] == "collect"
    assert "from_" not in serialized["edges"][0]


def test_unknown_fields_are_rejected() -> None:
    instance = json.loads(fixture("workflows/research.json"))
    instance["unexpected"] = True
    with pytest.raises(ValidationError):
        parse_workflow(json.dumps(instance))


def test_workflow_edge_contract() -> None:
    edge = WorkflowEdge.model_validate({"from": "collect", "to": "plan", "when": "true"})
    assert edge.from_ == "collect"
    assert edge.model_dump(by_alias=True)["from"] == "collect"
