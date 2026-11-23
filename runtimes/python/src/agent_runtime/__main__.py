"""Runtime process entry point.

Phase 02 exposes `version` for image smoke tests and container health verification. `serve` is the Phase 03
Unix-socket gRPC server and intentionally refuses to run before it exists.
"""

from __future__ import annotations

import argparse
import json
import sys

RUNTIME_ABI = 1
RUNTIME_VERSION = "0.1.0"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="agent_runtime")
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("version", help="print runtime ABI and version as JSON")
    serve = sub.add_parser("serve", help="serve the runtime over a Unix socket (Phase 03)")
    serve.add_argument("--socket", required=True)

    args = parser.parse_args(argv)

    if args.command == "version":
        print(json.dumps({"runtime": "agent_runtime", "version": RUNTIME_VERSION, "abi": RUNTIME_ABI}))
        return 0

    print("agent_runtime serve is implemented in Phase 03", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
