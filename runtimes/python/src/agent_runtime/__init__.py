"""Agent execution runtime.

The worker starts one process per task attempt and calls into this package. The runtime is a pure function of
its input plus credentials: it cannot read or write platform state.
"""

from agent_runtime.langgraph_agent import EchoState, build_echo_graph, run_echo_graph
from agent_runtime.logging import configure_logging
from agent_runtime.runtime import AgentRuntime, EchoAgentRuntime
from agent_runtime.spec import AgentSpec, NodeContext, NodeResult, RuntimeErrorInfo

__all__ = [
    "AgentRuntime",
    "AgentSpec",
    "EchoAgentRuntime",
    "EchoState",
    "NodeContext",
    "NodeResult",
    "RuntimeErrorInfo",
    "build_echo_graph",
    "configure_logging",
    "run_echo_graph",
]
