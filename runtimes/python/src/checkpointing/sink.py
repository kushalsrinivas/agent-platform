"""Checkpoint sink.

The runtime writes one atomic file per checkpoint into a private directory owned by the worker process. The worker
uploads each file over the fenced control RPC and owns durability; the runtime never sees database or object-store
credentials. Files are gzip-compressed canonical JSON from the shared codec, never pickle.
"""

from __future__ import annotations

import contextlib
import logging
import os
import tempfile
from pathlib import Path
from threading import Lock

from checkpointing.codec import Checkpoint, CheckpointError, encode, sha256

log = logging.getLogger(__name__)

CHECKPOINT_DIR_ENV = "AGENT_RUNTIME_CHECKPOINT_DIR"


class CheckpointSink:
    """Writes ordered checkpoint files. A failure never corrupts an earlier checkpoint."""

    def __init__(self, directory: Path, agent_version_id: str) -> None:
        self._directory = directory
        self._agent_version_id = agent_version_id
        self._sequence = 0
        self._lock = Lock()

    @classmethod
    def from_env(cls, agent_version_id: str) -> CheckpointSink | None:
        raw = os.environ.get(CHECKPOINT_DIR_ENV, "").strip()
        if not raw:
            return None
        directory = Path(raw)
        try:
            directory.mkdir(parents=True, mode=0o700, exist_ok=True)
        except OSError as exc:
            raise CheckpointError(f"checkpoint directory is not writable: {exc}") from exc
        return cls(directory, agent_version_id)

    def write(self, state: dict[str, object]) -> str | None:
        """Encode and atomically publish one checkpoint. Returns the file name."""
        with self._lock:
            self._sequence += 1
            sequence = self._sequence
        try:
            data = encode(Checkpoint(state=state, agent_version_id=self._agent_version_id or None))
        except (CheckpointError, ValueError, TypeError) as exc:
            raise CheckpointError(f"checkpoint could not be encoded: {exc}") from exc
        name = f"ckpt-{sequence:06d}-{sha256(data)}.json.gz"
        target = self._directory / name
        temp_name: str | None = None
        try:
            with tempfile.NamedTemporaryFile(
                mode="wb", dir=self._directory, prefix=".ckpt-", suffix=".tmp", delete=False
            ) as handle:
                temp_name = handle.name
                handle.write(data)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temp_name, target)
            temp_name = None
        except OSError as exc:
            raise CheckpointError(f"checkpoint could not be written: {exc}") from exc
        finally:
            if temp_name is not None:
                with contextlib.suppress(OSError):
                    os.unlink(temp_name)
        return name


__all__ = ["CHECKPOINT_DIR_ENV", "CheckpointSink"]
