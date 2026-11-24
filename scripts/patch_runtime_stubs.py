"""Post-process generated Python runtime stubs.

grpcio-tools emits imports for the proto package path, and the generated runtime module does not import the
well-known-type modules that register the Struct/Timestamp descriptors. Rewrite both so the stubs import inside
``agent_runtime.proto_gen``.
"""

from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GEN = ROOT / "runtimes" / "python" / "src" / "agent_runtime" / "proto_gen"


def main() -> None:
    grpc = GEN / "runtime_pb2_grpc.py"
    source = grpc.read_text()
    source = source.replace(
        "from runtime.v1 import runtime_pb2 as runtime_dot_v1_dot_runtime__pb2",
        "from . import runtime_pb2 as runtime__pb2",
    )
    source = source.replace("runtime_dot_v1_dot_runtime__pb2", "runtime__pb2")
    grpc.write_text(source)

    messages = GEN / "runtime_pb2.py"
    code = messages.read_text()
    if "struct_pb2" not in code:
        code = code.replace(
            "from google.protobuf.internal import builder as _builder\n",
            "from google.protobuf.internal import builder as _builder\n"
            "from google.protobuf import struct_pb2 as google_dot_protobuf_dot_struct__pb2\n"
            "from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2\n",
        )
        messages.write_text(code)

    worker_grpc = GEN / "worker_pb2_grpc.py"
    if worker_grpc.exists():
        source = worker_grpc.read_text()
        source = source.replace(
            "from worker.v1 import worker_pb2 as worker_dot_v1_dot_worker__pb2",
            "from . import worker_pb2 as worker__pb2",
        )
        source = source.replace("worker_dot_v1_dot_worker__pb2", "worker__pb2")
        worker_grpc.write_text(source)

    worker_messages = GEN / "worker_pb2.py"
    if worker_messages.exists():
        code = worker_messages.read_text()
        if "struct_pb2" not in code:
            code = code.replace(
                "from google.protobuf.internal import builder as _builder\n",
                "from google.protobuf.internal import builder as _builder\n"
                "from google.protobuf import struct_pb2 as google_dot_protobuf_dot_struct__pb2\n"
                "from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2\n",
            )
            worker_messages.write_text(code)

    (GEN / "__init__.py").write_text(
        '"""Generated runtime ABI stubs. Regenerate with `make proto`. Do not edit by hand."""\n'
    )


if __name__ == "__main__":
    main()
