"""Event envelope and payload contract tests."""

from __future__ import annotations

import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker

from contracts import SupportedEventTypes, parse_event

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = ROOT / "packages" / "test-fixtures"
SCHEMAS = ROOT / "packages" / "schemas"
ENVELOPE = ROOT / "docs" / "api" / "envelope.schema.json"


def validator_for(schema_path: Path) -> Draft202012Validator:
    schema = json.loads(schema_path.read_text())
    return Draft202012Validator(schema, format_checker=FormatChecker())


def test_all_event_fixtures_validate_and_parse() -> None:
    envelope_validator = validator_for(ENVELOPE)
    for path in sorted((FIXTURES / "events").glob("*.json")):
        data = path.read_bytes()
        instance = json.loads(data)
        envelope_errors = list(envelope_validator.iter_errors(instance))
        assert envelope_errors == [], f"{path.name} envelope: {envelope_errors}"

        schema_rel = SupportedEventTypes[instance["message_type"]]
        payload_validator = validator_for(SCHEMAS / schema_rel)
        payload_errors = list(payload_validator.iter_errors(instance["payload"]))
        assert payload_errors == [], f"{path.name} payload: {payload_errors}"

        parsed = parse_event(data)
        assert parsed.envelope.message_type == instance["message_type"]


def test_invalid_counts_are_rejected() -> None:
    instance = json.loads((FIXTURES / "events" / "execution.created.json").read_text())
    instance["payload"]["counts"]["pending"] = 99
    schema_validator = validator_for(SCHEMAS / "events/execution.created.schema.json")
    # JSON Schema cannot express total == sum; the typed model must.
    assert list(schema_validator.iter_errors(instance["payload"])) == []
    try:
        parse_event(json.dumps(instance))
    except ValueError:
        pass
    else:
        raise AssertionError("counts total mismatch must be rejected")
