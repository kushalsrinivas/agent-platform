"""Checkpoint encoding.

Checkpoints are the only Python-authored durable artifact. They are gzip-compressed canonical JSON with an
explicit schema version and a hard size cap, so the worker can enforce the same limit before upload.
"""

from checkpointing.codec import (
    CHECKPOINT_SCHEMA_VERSION,
    MAX_CHECKPOINT_BYTES,
    Checkpoint,
    CheckpointError,
    decode,
    encode,
    sha256,
)

__all__ = [
    "CHECKPOINT_SCHEMA_VERSION",
    "MAX_CHECKPOINT_BYTES",
    "Checkpoint",
    "CheckpointError",
    "decode",
    "encode",
    "sha256",
]
