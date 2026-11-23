from datetime import UTC, datetime

from checkpointing import CheckpointError, decode, encode, sha256
from checkpointing.codec import MAX_CHECKPOINT_BYTES, Checkpoint


def test_roundtrip_is_deterministic() -> None:
    checkpoint = Checkpoint(
        agent_version_id="agtv_01",
        created_at=datetime(2026, 1, 1, tzinfo=UTC),
        state={"messages": [{"role": "user", "content": "hi"}], "steps": 2},
    )

    first = encode(checkpoint)
    second = encode(checkpoint)

    assert first == second
    assert sha256(first) == sha256(second)
    assert decode(first).state["steps"] == 2


def test_rejects_unknown_schema() -> None:
    data = encode(Checkpoint(state={"a": 1}))
    # Corrupt the version by re-encoding a mismatched model.
    import gzip
    import json

    payload = json.loads(gzip.decompress(data))
    payload["schema_version"] = 99
    bad = gzip.compress(json.dumps(payload).encode())

    try:
        decode(bad)
    except CheckpointError as exc:
        assert "not supported" in str(exc)
    else:
        raise AssertionError("expected CheckpointError")


def test_rejects_corrupt_bytes() -> None:
    try:
        decode(b"not gzip")
    except CheckpointError:
        pass
    else:
        raise AssertionError("expected CheckpointError")


def test_rejects_oversized_payload() -> None:
    data = b"\0" * (MAX_CHECKPOINT_BYTES + 1)
    try:
        decode(data)
    except CheckpointError as exc:
        assert "limit" in str(exc)
    else:
        raise AssertionError("expected CheckpointError")
