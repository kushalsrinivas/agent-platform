"""Versioned checkpoint codec."""

from __future__ import annotations

import gzip
import hashlib
import json
from datetime import UTC, datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

CHECKPOINT_SCHEMA_VERSION = 1
MAX_CHECKPOINT_BYTES = 32 * 1024 * 1024  # Matches the runtime proto limit.


class Checkpoint(BaseModel):
    model_config = ConfigDict(frozen=True)

    schema_version: int = CHECKPOINT_SCHEMA_VERSION
    agent_version_id: str | None = None
    created_at: datetime = Field(default_factory=lambda: datetime.now(UTC))
    state: dict[str, Any]


class CheckpointError(ValueError):
    """Raised when bytes cannot be treated as a checkpoint of this schema."""


def encode(checkpoint: Checkpoint) -> bytes:
    """Serialise deterministically so identical state produces an identical sha256."""
    raw = json.dumps(
        checkpoint.model_dump(mode="json"),
        sort_keys=True,
        separators=(",", ":"),
    ).encode()
    data = gzip.compress(raw, mtime=0)
    if len(data) > MAX_CHECKPOINT_BYTES:
        msg = f"checkpoint is {len(data)} bytes; limit is {MAX_CHECKPOINT_BYTES}"
        raise CheckpointError(msg)
    return data


def decode(data: bytes, *, expected_schema_version: int = CHECKPOINT_SCHEMA_VERSION) -> Checkpoint:
    if len(data) > MAX_CHECKPOINT_BYTES:
        msg = f"checkpoint is {len(data)} bytes; limit is {MAX_CHECKPOINT_BYTES}"
        raise CheckpointError(msg)
    try:
        raw = gzip.decompress(data)
        payload = json.loads(raw)
        checkpoint = Checkpoint.model_validate(payload)
    except (OSError, ValueError, TypeError) as exc:
        raise CheckpointError("checkpoint is not valid compressed JSON") from exc

    if checkpoint.schema_version != expected_schema_version:
        raise CheckpointError(
            f"checkpoint schema {checkpoint.schema_version} is not supported (expected {expected_schema_version})"
        )
    return checkpoint


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()
