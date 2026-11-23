# Phase 07 — Python agent runtime

Status: complete. This phase delivers the execution environment behind a task attempt: a LangGraph agent loop,
provider adapters, structured results, usage extraction, cancellation and a runtime ABI served over a Unix socket.
The Go worker owns leases, NATS and result reporting; Python owns only the agent execution.

The exit criterion is met and tested against the live stack:

> A workflow task dispatched by Go executes a real LangGraph agent through a Python worker and returns a validated
> result to the Go orchestrator.

## 1. Runtime lifecycle

The Phase 06 worker lifecycle is unchanged at the top:

```
worker start → load configuration → register with the control plane → subscribe to TASK_DISPATCH
→ receive a dispatch → validate the assignment → fetch task context → start one Python process
→ initialize the agent → execute → validate output → report the fenced result → wait for the next task
```

The Python process is per attempt. The Go worker:

1. validates the dispatch envelope, tenant, worker and lease;
2. fetches the immutable context with `GetTaskContext` (pinned agent spec, execution input, node params, upstream
   outputs, deadline, node output schema);
3. creates a private temp directory and Unix socket;
4. starts `python -m agent_runtime serve --socket <path>` in its own process group;
5. calls the runtime ABI with a deadline;
6. validates the output against the node's `output_schema`;
7. reports the result through `ReportTaskResult` and only then acknowledges the broker message;
8. terminates the process group (`SIGTERM`, 5 s grace, `SIGKILL`) on every exit path.

A runtime start failure or crash is an infrastructure failure for that attempt; the lease and retry policy own
recovery. A classified runtime failure is reported as a task failure without being retried as a message.

## 2. Agent runtime interface

`agent_runtime.runtime.AgentRuntime` is the standard contract:

```python
class AgentRuntime(ABC):
    async def initialize(self, spec, context) -> None: ...
    async def execute(self, input_data, context) -> NodeResult: ...
    async def get_state(self) -> dict: ...
    async def restore_state(self, checkpoint) -> None: ...
    async def cancel(self) -> None: ...
    async def shutdown(self) -> None: ...
```

`LangGraphRuntime` implements it today; `EchoAgentRuntime` is the deterministic test/offline adapter. A future
framework implements the same six methods and is selected by the adapter factory; the Go worker never changes.

`NodeResult` carries `outcome ∈ {succeeded, failed, cancelled}`, the output object, optional checkpoint bytes,
token usage, steps, model and a sanitised error.

## 3. Model providers

Providers are plain HTTP adapters with a declared capability set. They are not forced into identical shapes.

| Provider | Wire format | Streaming | Tools | Structured output |
|---|---|---|---|---|
| `echo` | deterministic in-process | yes | yes (add tool) | yes |
| `openai_compat` (`openai`, compatible gateways) | Chat Completions | yes (SSE) | yes (function calling) | yes (`response_format` JSON Schema) |
| `anthropic` | Messages API | yes (SSE) | yes (`tool_use`) | no — the API has no first-class response schema; the adapter raises `unsupported_capability` |
| `gemini` | `generateContent` / `streamGenerateContent` | yes (SSE) | yes (`functionDeclarations`) | yes (`responseMimeType` + `responseSchema`) |

The common surface covers non-streaming `complete`, streaming events, tool calls, usage extraction, timeout
normalisation and error normalisation (`rate_limited`, `provider_unavailable`, `provider_rejected`, `timeout`,
`transport`, `invalid_response`, `unknown_provider`, `unsupported_capability`). Rate limits and 5xx are marked
retryable; 4xx configuration/content failures are not. Provider response bodies are never attached to errors.

Credentials are task-scoped and named by `model.credential_ref`. No credential store is wired yet: the runtime
reads credentials from the context map, and a missing key yields a configuration failure at model call time. The
OpenAI-compatible adapter also accepts a `base_url` in the agent model parameters for local gateways.

## 4. LangGraph adapter

`LangGraphRuntime` builds and invokes a `StateGraph`:

- **State schema:** messages (LangChain messages with tool calls), step counter, output, halted flag.
- **Nodes:** `agent` (model request), `tools` (platform tool execution).
- **Control flow:** `START → agent → tools → agent … → END`; the loop ends when the model stops requesting tools
  or the turn budget is exhausted.
- **Tools:** resolved from the pinned agent spec bindings against the runtime image's tool registry. The built-ins
  are deterministic and side-effect free: `echo`, `add`, and a stable `tool_web_search` stand-in; the last one lets
  the existing tool-node workflow execute end to end without network egress.
- **Streaming events:** provider streaming is implemented and unit-tested; the runtime emits structured events
  (`model_request_started`, `model_response_received`, `tool_invocation_started/completed`) to stdout while the
  current ABI call remains unary.
- **Checkpoints:** `get_state` serialises the message history and step count through the Phase 03 checkpoint codec
  (gzip JSON, 32 MiB limit); `restore_state` validates the agent version and continues the conversation. The
  runtime returns checkpoint bytes on success; durable upload to object storage is deferred (see deviations).
- **Errors:** provider failures map to classified `NodeResult.failure`; `MAX_TURNS_EXCEEDED`, `TASK_TIMEOUT`,
  `CHECKPOINT_INVALID` and `RUNTIME_ERROR` are stable codes. Unexpected exceptions never escape as an RPC error.
- **Cancellation:** `cancel()` sets a cooperative flag checked before each model call; the worker additionally
  terminates the process group, so cancellation never depends on the graph cooperating.

## 5. Structured execution results

Every task returns the standard shape:

```json
{
  "status": "completed",
  "output": { "report": "Generated research report", "steps": 2, "model": "echo-1" },
  "usage": { "input_tokens": 235, "output_tokens": 148 },
  "metadata": { "provider": "echo", "model": "echo-1" }
}
```

Inside the platform the runtime returns a `NodeResult`; the worker maps it to the worker result contract. Output is
validated against the pinned node `output_schema` **before** the result is reported: an invalid object is reported
as `RESULT_SCHEMA_INVALID` (permanent), never as a task success. The attempt result stores the inline output and
metrics (`duration_ms`, `tokens_in`, `tokens_out`, `steps`, `extra`), and the runtime version.

## 6. Concurrency and resource limits

| Bound | Mechanism |
|---|---|
| Concurrent tasks per worker | Worker leases + `WORKER_MAX_CONCURRENCY`; one runtime process per attempt. |
| Concurrent model calls | One task per process and one in-flight model call per graph node; the attempt deadline bounds total turns. |
| Tool calls per task | Bounded by `limits.max_turns`; a task cannot exceed it (fails `MAX_TURNS_EXCEEDED`). |
| Execution timeout | Node timeout from the pinned graph, capped by the execution deadline; enforced by the worker context and the runtime deadline check. |
| Cancellation | Cooperative flag plus process-group `SIGTERM`/`SIGKILL`; the broker ack is withheld if the attempt is interrupted. |
| Memory | Process-level sampling exists; hard cgroup limits are a deployment concern and are documented, not enforced by the runtime yet. |

## 7. Observability

The Python runtime emits one JSON line per event, forwarded by the worker to its stdout:

`task_started`, `agent_initialized`, `model_request_started`, `model_response_received`,
`tool_invocation_started`, `tool_invocation_completed`, `agent_completed`, `task_failed`.

Every line carries `execution_id`, `task_id`, `attempt_id`, `node_id` and `event`. The Go worker also logs task
start/result/failure and sends advisory `TaskProgress` messages on the control stream. Live token streaming to the
control plane is deferred until the runtime ABI grows a streaming RPC.

## 8. Configuration

| Setting | Default | Meaning |
|---|---|---|
| `WORKER_RUNTIME_COMMAND` | `python -m agent_runtime serve --socket {socket}` in Compose; empty on a bare Go worker | Runtime process command; `{socket}` is replaced with the per-attempt socket path. Empty falls back to the offline echo executor. |
| `WORKER_RUNTIME_STARTUP_SECONDS` | `15` | How long the worker waits for the runtime socket. |
| `WORKER_RUNTIME_KILL_SECONDS` | `5` | Grace between `SIGTERM` and `SIGKILL` for the process group. |
| `WORKER_RUNTIME` | `python` | Runtime family advertised at registration. |
| `WORKER_RUNTIME_VERSION` | `0.1.0` | Runtime version advertised and returned by the runtime. |
| `WORKER_RUNTIME_ABI` | `1` | Runtime ABI passed to `ExecuteNode`. |

The worker image (`infrastructure/docker/worker.Dockerfile`) contains both the Go host and the Python runtime venv,
so `python` resolves inside the image.

## 9. Verification

```bash
make test                 # Go + Python unit tests
make lint                 # gofmt, vet, golangci-lint, Ruff, mypy, Biome
make integration-test     # Compose stack, host Python venv, full suite
```

Python coverage (52 tests) includes:

- runtime contract, spec parsing and cancellation;
- LangGraph loop, tool loop, checkpoints/resume, max turns, deadline;
- provider wire formats, tool calls, structured outputs, usage, rate limits, 5xx/4xx, timeouts, streaming for echo,
  OpenAI-compatible, Anthropic and Gemini via `httpx.MockTransport`;
- runtime ABI over a real Unix socket, including malformed specs, unknown providers and the tool runtime.

Go coverage includes `splitCommand`, output-schema validation and the Phase 04–06 suites. Integration:

- `TestRuntimeLangGraphAgentExecution` — API-submitted execution → scheduler → Go worker → Python LangGraph agent
  → output schema validation → `ReportTaskResult` → execution `succeeded`; asserts the echo report, runtime version,
  token usage and the `task.attempt_finished` event.
- `TestRuntimeOutputSchemaRejectsInvalidOutput` — a schema the output cannot satisfy produces
  `RESULT_SCHEMA_INVALID` and a failed execution, never a false success.

The complete suite passes with the Compose worker running the Python executor and with host test workers using the
local venv.

## 10. Deliberate deviations and follow-ups

| Item | Status | Reason / trigger |
|---|---|---|
| Python process subscribes to NATS | No | The accepted architecture gives the Go worker the NATS consumer and lease; the Python process gets one attempt over a UDS and has no bus or DB credentials. |
| Live token streaming to the control plane | Deferred | The runtime ABI is unary; structured events are emitted to logs instead. Add a streaming RPC when the dashboard needs token-level updates. |
| Durable checkpoint upload | Deferred | `CreateArtifactUpload`/presigned object storage is not wired. The runtime produces and can restore checkpoint bytes; the worker logs and drops them today. |
| Model credentials | Empty | No secret store exists; the runtime reads task-scoped credentials but the orchestrator returns none. Real provider calls therefore need credentials injection in a later slice. |
| Hard memory/CPU limits | Deployment concern | The runtime samples usage; cgroup/no-new-privileges/egress policy is the sandbox phase. |
| Provider SDKs | Not used | Plain HTTP keeps the image small and makes every wire format testable with `httpx.MockTransport`. |
| Anthropic structured outputs | Unsupported by design | The Messages API has no response schema; callers must not assume identical capabilities across providers. |
| MCP tools | Interface only | The Phase 03 MCP client protocol exists; no live MCP server is configured. |
| Multi-agent/LangGraph subgraphs | Single graph | The adapter builds the agent loop; composing workflow nodes into one LangGraph is not needed because the platform workflow graph is the orchestration layer. |
