"""Runtime process entry point.

``serve`` binds the runtime ABI on a per-attempt Unix socket; the Go worker owns the process lifecycle and
terminates the process group on cancellation.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys

from agent_runtime import telemetry
from agent_runtime.grpc_service import RUNTIME_ABI, RUNTIME_VERSION, serve
from agent_runtime.logging import configure_logging


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="agent_runtime")
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("version", help="print runtime ABI and version as JSON")
    serve_parser = sub.add_parser("serve", help="serve the runtime ABI on a Unix socket")
    serve_parser.add_argument("--socket", required=True)

    args = parser.parse_args(argv)

    if args.command == "version":
        print(json.dumps({"runtime": "agent_runtime", "version": RUNTIME_VERSION, "abi": RUNTIME_ABI}))
        return 0

    configure_logging()
    telemetry.configure()
    try:
        asyncio.run(serve(args.socket))
    except KeyboardInterrupt:
        return 0
    except Exception as exc:
        print(f"agent_runtime serve failed: {exc}", file=sys.stderr)
        return 1
    finally:
        telemetry.shutdown()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
