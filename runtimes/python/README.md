# Python runtime

One distribution (`agent-platform-runtime`) that exposes the packages the worker runs inside an attempt sandbox:

| Package | Responsibility |
|---|---|
| `agent_runtime` | Agent spec models, the LangGraph-backed agent loop, structured logging, result types. |
| `model_providers` | Provider protocol plus an OpenAI-compatible HTTP provider and a deterministic test provider. |
| `tool_runtime` | Versioned platform-provided tools and the executor the runtime calls. |
| `mcp` | MCP server/tool descriptors and the client protocol. Transport lands with MCP support. |
| `checkpointing` | Versioned, compressed, size-limited agent state serialisation. |
| `contracts` | Workflow, task and event models mirroring `packages/contracts` (Go) and validated against the shared JSON Schemas. |

Rules that the runtime must not break:

- No database, NATS or object-store credentials. The worker owns all I/O across the trust boundary.
- Every entry point is async and honours a deadline/cancellation from the worker.
- Outputs and checkpoints are JSON-serialisable; checkpoints declare a schema version.
- Logs go to stderr/stdout as JSON. Secrets are never logged.

## Commands

```bash
uv sync                     # create .venv and install
uv run pytest               # tests
uv run ruff check .
uv run ruff format --check .
uv run mypy
```
