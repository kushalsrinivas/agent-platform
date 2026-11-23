"""LangGraph agent loop.

The graph is the unit of checkpointable state. Phase 02 ships a single deterministic node so the dependency,
state model and test harness are real; LLM and tool nodes plug into the same graph in Phase 03.
"""

from __future__ import annotations

from typing import TypedDict

from langgraph.graph import END, START, StateGraph
from langgraph.graph.state import CompiledStateGraph


class EchoState(TypedDict, total=False):
    """State carried between nodes and checkpointed between super-steps."""

    input: str
    output: str
    steps: int


def _respond(state: EchoState) -> EchoState:
    text = state.get("input", "")
    return {"output": text, "steps": state.get("steps", 0) + 1}


def build_echo_graph() -> CompiledStateGraph[EchoState, None, EchoState, EchoState]:
    """Compile the deterministic baseline graph used by tests and the local demo."""
    graph = StateGraph(EchoState)
    graph.add_node("respond", _respond)
    graph.add_edge(START, "respond")
    graph.add_edge("respond", END)
    return graph.compile()


async def run_echo_graph(text: str) -> EchoState:
    """Async wrapper so the runtime surface is uniformly async."""
    result = await build_echo_graph().ainvoke({"input": text})
    return {"input": text, "output": result["output"], "steps": result["steps"]}
