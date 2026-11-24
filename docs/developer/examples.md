# Examples

Runnable examples live in `examples/python` in the repository. They default to the deterministic `echo`
provider so they run without a model credential. Set `MODEL_PROVIDER`, `MODEL_NAME`, `MODEL_CREDENTIAL` and
`RUNTIME_DIGEST` for a real model/runtime.

| # | Example | File |
|---|---|---|
| 1 | Simple LLM agent | `01_simple_llm_agent.py` |
| 2 | Research agent with web search | `02_research_agent.py` |
| 3 | Multi-agent sequential workflow | `03_multi_agent_workflow.py` |
| 4 | Parallel branches and join | `04_parallel_execution.py` |
| 5 | Conditional CEL routing | `05_conditional_workflow.py` |
| 6 | MCP-enabled agent | `06_mcp_agent.py` |
| 7 | Human approval | `07_human_approval.py` |
| 8 | Scheduled workflow | `08_scheduled_workflow.py` |
| 9 | Long-running durable delay | `09_long_running_workflow.py` |
| 10 | Cancellation and resume | `10_error_recovery.py` |

Setup:

```bash
export AGENT_PLATFORM_URL=http://localhost:8080
export AGENT_PLATFORM_API_KEY=apk_...
export AGENT_PLATFORM_ORG=org_...
export AGENT_PLATFORM_PROJECT=proj_...
python examples/python/01_simple_llm_agent.py
```

`06_mcp_agent.py` starts the stdio fixture server from `runtimes/python/tests/mcp_test_server.py`. If the worker
runs in a different filesystem namespace (for example a container), set the MCP command/args to a path visible
inside that worker.

`08_scheduled_workflow.py` is a complete schedule driver: run it under cron/systemd or use `--once` from CI. The
idempotency key is the schedule slot, so a retried tick never duplicates an execution.

`10_error_recovery.py` cancels a durable delay, then `resume`s the execution. Resume creates a new execution and
reuses only outputs that succeeded.
