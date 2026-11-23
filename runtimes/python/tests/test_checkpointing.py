from datetime import UTC, datetime
from pathlib import Path

import pytest

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


def test_sink_writes_ordered_atomic_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("AGENT_RUNTIME_CHECKPOINT_DIR", str(tmp_path))
    from checkpointing import CheckpointSink

    sink = CheckpointSink.from_env("agtv_01")
    assert sink is not None
    first = sink.write({"messages": [], "steps": 1, "completed": False})
    second = sink.write({"messages": [], "steps": 2, "completed": True})

    names = sorted(path.name for path in tmp_path.iterdir())
    assert names == [first, second]
    assert names[0].startswith("ckpt-000001-")
    assert names[1].startswith("ckpt-000002-")
    assert not list(tmp_path.glob("*.tmp"))
    data = (tmp_path / names[1]).read_bytes()
    assert decode(data).state["completed"] is True
    assert sha256(data) in names[1]
