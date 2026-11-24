# Python SDK examples

All examples use `platform_sdk` and default to the deterministic `echo` provider.

```bash
export AGENT_PLATFORM_URL=http://localhost:8080
export AGENT_PLATFORM_API_KEY=apk_...
export AGENT_PLATFORM_ORG=org_...
export AGENT_PLATFORM_PROJECT=proj_...
python examples/python/01_simple_llm_agent.py
```

| File | Demonstrates |
|---|---|
| `01_simple_llm_agent.py` | Agent deployment and one-node workflow |
| `02_research_agent.py` | Web-search tool plus agent |
| `03_multi_agent_workflow.py` | Three specialist agents in sequence |
| `04_parallel_execution.py` | Fan-out and join-all |
| `05_conditional_workflow.py` | CEL conditional routing |
| `06_mcp_agent.py` | stdio MCP discovery and invocation |
| `07_human_approval.py` | Approval policy, assignment and resume |
| `08_scheduled_workflow.py` | Idempotent scheduled execution |
| `09_long_running_workflow.py` | Durable delay |
| `10_error_recovery.py` | Cancel then resume |

See [docs/developer/examples.md](../docs/developer/examples.md) for notes.
