"""Agent execution runtime.

The worker starts one process per task attempt and calls into this package. The runtime is a pure function of
its input plus credentials: it cannot read or write platform state.
"""

from agent_runtime.events import EventEmitter
from agent_runtime.grpc_service import (
    RUNTIME_ABI,
    RUNTIME_VERSION,
    AgentRuntimeServicer,
    ToolRuntimeServicer,
    serve,
)
from agent_runtime.langgraph_runtime import LangGraphRuntime
from agent_runtime.logging import configure_logging
from agent_runtime.runtime import AgentRuntime, EchoAgentRuntime
from agent_runtime.spec import (
    AgentVersionSpec,
    NodeContext,
    NodeResult,
    RuntimeErrorInfo,
)

__all__ = [
    "RUNTIME_ABI",
    "RUNTIME_VERSION",
    "AgentRuntime",
    "AgentRuntimeServicer",
    "AgentVersionSpec",
    "EchoAgentRuntime",
    "EventEmitter",
    "LangGraphRuntime",
    "NodeContext",
    "NodeResult",
    "RuntimeErrorInfo",
    "ToolRuntimeServicer",
    "configure_logging",
    "serve",
]
