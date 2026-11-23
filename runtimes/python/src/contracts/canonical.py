"""RFC 8785 canonical JSON and content digests.

The digest is computed over the graph (nodes, edges, policy, input_schema) and is the value stored on
workflow_versions. The document digest is used by cross-language tests and may be shown as a spec identifier.
"""

from __future__ import annotations

import hashlib
from typing import Any

import rfc8785
from pydantic import BaseModel


class CanonicalError(ValueError):
    """Input is not representable as RFC 8785 canonical JSON."""


def _payload(value: BaseModel | dict[str, Any]) -> dict[str, Any]:
    if isinstance(value, BaseModel):
        return value.model_dump(mode="json", exclude_none=True)
    return value


def canonicalize(value: BaseModel | dict[str, Any]) -> bytes:
    try:
        return rfc8785.dumps(_payload(value))
    except (TypeError, ValueError) as exc:
        raise CanonicalError(f"value is not canonicalisable: {exc}") from exc


def sha256_digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def digest(value: BaseModel | dict[str, Any]) -> str:
    return sha256_digest(canonicalize(value))


def graph_payload(document: BaseModel) -> dict[str, Any]:
    """Return only the persisted graph fields in canonical form."""
    return document.model_dump(
        mode="json",
        exclude_none=True,
        include={"nodes", "edges", "policy", "input_schema"},
    )
