"""LangGraph adapter.

The graph is the unit of checkpointable state: one ``agent`` node calls the model, one ``tools`` node executes
bound platform tools, and a conditional edge loops until the model stops asking for tools or the turn budget is
exhausted. LangGraph-specific detail stops at this module; the runtime contract above it stays framework-agnostic.
"""

from __future__ import annotations

import asyncio
import json
from datetime import UTC, datetime
from typing import Annotated, Any, TypedDict

from langchain_core.messages import (
    AIMessage,
    BaseMessage,
    HumanMessage,
    SystemMessage,
    ToolMessage,
    message_to_dict,
    messages_from_dict,
)
from langgraph.graph import END, START, StateGraph
from langgraph.graph.message import add_messages
from langgraph.graph.state import CompiledStateGraph

from agent_runtime.events import EventEmitter
from agent_runtime.runtime import AgentRuntime
from agent_runtime.spec import AgentVersionSpec, NodeContext, NodeResult
from checkpointing.codec import Checkpoint, CheckpointError, encode
from model_providers.base import Completion, Message, ProviderConfig, ProviderError, ToolCall, ToolSpec
from model_providers.registry import get_provider
from tool_runtime import ToolRegistry, default_registry


class AgentState(TypedDict, total=False):
    """State carried between super-steps and checkpointed at node end."""

    messages: Annotated[list[BaseMessage], add_messages]
    steps: int
    output: str
    halted: bool


class _DeadlineExceeded(Exception):
    pass


class _Cancelled(Exception):
    pass


def _to_langchain(messages: list[Message]) -> list[BaseMessage]:
    converted: list[BaseMessage] = []
    for message in messages:
        match message.role:
            case "system":
                converted.append(SystemMessage(content=message.content))
            case "user":
                converted.append(HumanMessage(content=message.content))
            case "assistant":
                converted.append(
                    AIMessage(
                        content=message.content,
                        tool_calls=[
                            {"name": call.name, "args": call.arguments, "id": call.id, "type": "tool_call"}
                            for call in message.tool_calls
                        ],
                    )
                )
            case "tool":
                converted.append(
                    ToolMessage(
                        content=message.content, tool_call_id=message.tool_call_id or "", name=message.name or ""
                    )
                )
    return converted


def _from_langchain(messages: list[BaseMessage]) -> list[Message]:
    converted: list[Message] = []
    for message in messages:
        if isinstance(message, SystemMessage):
            converted.append(Message(role="system", content=str(message.content)))
        elif isinstance(message, HumanMessage):
            converted.append(Message(role="user", content=str(message.content)))
        elif isinstance(message, AIMessage):
            converted.append(
                Message(
                    role="assistant",
                    content=str(message.content),
                    tool_calls=tuple(
                        ToolCall(
                            id=str(call.get("id", "")),
                            name=str(call.get("name", "")),
                            arguments=dict(call.get("args", {})),
                        )
                        for call in (message.tool_calls or [])
                    ),
                )
            )
        elif isinstance(message, ToolMessage):
            converted.append(
                Message(
                    role="tool",
                    content=str(message.content),
                    tool_call_id=str(message.tool_call_id),
                    name=str(message.name),
                )
            )
    return converted


def _user_payload(input_data: dict[str, Any], context: NodeContext) -> str:
    payload: dict[str, Any] = {"input": input_data, "params": context.node_params}
    if context.upstream_outputs:
        payload["upstream"] = context.upstream_outputs
    return json.dumps(payload, sort_keys=True)


class LangGraphRuntime(AgentRuntime):
    """Runs one pinned agent version as a bounded LangGraph loop."""

    def __init__(self, *, emitter: EventEmitter | None = None, tools: ToolRegistry | None = None) -> None:
        self._emitter = emitter
        self._tools = tools or default_registry()
        self._spec: AgentVersionSpec | None = None
        self._spec_version_id = ""
        self._provider: Any | None = None
        self._provider_config = ProviderConfig()
        self._tool_specs: list[ToolSpec] = []
        self._graph: CompiledStateGraph[AgentState, None, AgentState, AgentState] | None = None
        self._input_tokens = 0
        self._output_tokens = 0
        self._cancelled = asyncio.Event()
        self._deadline: datetime | None = None
        self._restored_messages: list[BaseMessage] | None = None
        self._restored_steps = 0
        self._last_messages: list[BaseMessage] = []
        self._last_steps = 0

    # -- AgentRuntime contract -------------------------------------------------

    async def initialize(self, spec: AgentVersionSpec, context: NodeContext) -> None:
        self._spec = spec
        self._spec_version_id = context.agent_version_id
        provider = get_provider(spec.model.provider)
        self._provider = provider
        parameters = dict(spec.model.parameters)
        api_key = context.credentials.get(spec.model.credential_ref) or parameters.get("api_key")
        self._provider_config = ProviderConfig(
            model=spec.model.name,
            base_url=parameters.get("base_url"),
            api_key=api_key,  # type: ignore[arg-type]
            temperature=float(parameters.get("temperature", 0.0)),
            max_tokens=int(parameters.get("max_tokens", 1024)),
            timeout_seconds=float(parameters.get("timeout_seconds", 60.0)),
        )
        self._tool_specs = self._bind_tools(spec)
        self._graph = self._build_graph()
        if self._emitter:
            self._emitter.agent_initialized(context.agent_version_id, spec.model.provider, spec.model.name)

    async def execute(self, input_data: dict[str, Any], context: NodeContext) -> NodeResult:
        assert self._graph is not None and self._spec is not None
        self._input_tokens = 0
        self._output_tokens = 0
        self._deadline = context.deadline

        if self._restored_messages is not None:
            messages = list(self._restored_messages)
            steps = self._restored_steps
        else:
            messages = [SystemMessage(content=self._spec.instructions or "You are a helpful agent.")]
            messages.append(HumanMessage(content=_user_payload(input_data, context)))
            steps = 0

        try:
            final = await self._graph.ainvoke(
                {"messages": messages, "steps": steps},
                config={"recursion_limit": self._spec.limits.max_turns * 3 + 4},
            )
        except ProviderError as exc:
            return NodeResult.failure(exc.code, exc.message, retryable=exc.retryable)
        except _DeadlineExceeded:
            return NodeResult.failure("TASK_TIMEOUT", "agent execution exceeded its deadline", retryable=True)
        except _Cancelled:
            return NodeResult.cancelled()
        except CheckpointError as exc:
            return NodeResult.failure("CHECKPOINT_INVALID", str(exc))
        except (TypeError, ValueError) as exc:
            return NodeResult.failure("RUNTIME_ERROR", f"agent execution failed: {exc}")
        except Exception:
            return NodeResult.failure("RUNTIME_ERROR", "agent execution failed unexpectedly")

        self._last_messages = list(final.get("messages", []))
        self._last_steps = int(final.get("steps", 0))
        if final.get("halted"):
            return NodeResult.failure(
                "MAX_TURNS_EXCEEDED",
                f"agent did not finish within {self._spec.limits.max_turns} turns",
            )

        steps = self._last_steps
        text = str(final.get("output", ""))
        checkpoint = self._checkpoint(final)
        if self._emitter:
            self._emitter.agent_completed(steps, self._input_tokens, self._output_tokens)
        return NodeResult.success(
            {"report": text, "steps": steps, "model": self._provider_config.model},
            input_tokens=self._input_tokens,
            output_tokens=self._output_tokens,
            steps=steps,
            checkpoint=checkpoint,
            model=self._provider_config.model,
        )

    async def get_state(self) -> dict[str, Any]:
        return {
            "messages": [message_to_dict(message) for message in self._last_messages],
            "steps": self._last_steps,
        }

    async def restore_state(self, checkpoint: dict[str, Any]) -> None:
        if not checkpoint.get("state"):
            return
        if self._spec_version_id and checkpoint.get("agent_version_id") not in (None, "", self._spec_version_id):
            raise CheckpointError("checkpoint belongs to a different agent version")
        state = checkpoint["state"]
        try:
            restored = messages_from_dict(list(state.get("messages", [])))
        except (TypeError, ValueError) as exc:
            raise CheckpointError("checkpoint messages could not be restored") from exc
        self._restored_messages = [message for message in restored if isinstance(message, BaseMessage)]
        self._restored_steps = int(state.get("steps", 0))

    async def cancel(self) -> None:
        self._cancelled.set()

    async def shutdown(self) -> None:
        return None

    # -- internals --------------------------------------------------------------

    def _bind_tools(self, spec: AgentVersionSpec) -> list[ToolSpec]:
        specs: list[ToolSpec] = []
        for binding in spec.tools:
            tool = self._tools.get(binding.tool_id)
            if tool is None:
                raise ProviderError("unknown_tool", f"tool {binding.tool_id!r} is not available in this runtime image")
            specs.append(
                ToolSpec(
                    name=tool.name,
                    description=f"Platform tool {tool.name} v{tool.version}",
                    parameters={"type": "object"},
                )
            )
        return specs

    def _build_graph(self) -> CompiledStateGraph[AgentState, None, AgentState, AgentState]:
        graph = StateGraph(AgentState)
        graph.add_node("agent", self._call_model)
        graph.add_node("tools", self._run_tools)
        graph.add_edge(START, "agent")
        graph.add_conditional_edges("agent", self._route_after_agent, {"tools": "tools", "end": END})
        graph.add_conditional_edges("tools", self._route_after_tools, {"agent": "agent", "end": END})
        return graph.compile()

    def _route_after_agent(self, state: AgentState) -> str:
        messages = state.get("messages", [])
        last = messages[-1] if messages else None
        if isinstance(last, AIMessage) and last.tool_calls:
            return "tools"
        return "end"

    def _route_after_tools(self, state: AgentState) -> str:
        if state.get("halted"):
            return "end"
        return "agent"

    async def _call_model(self, state: AgentState) -> dict[str, Any]:
        assert self._provider is not None and self._spec is not None
        if self._cancelled.is_set():
            raise _Cancelled
        if self._deadline is not None and datetime.now(UTC) >= self._deadline:
            raise _DeadlineExceeded

        steps = int(state.get("steps", 0)) + 1
        if self._emitter:
            self._emitter.model_request_started(self._spec.model.provider, self._provider_config.model, steps)

        completion: Completion = await self._provider.complete(
            _from_langchain(state.get("messages", [])),
            self._provider_config,
            tools=self._tool_specs or None,
        )
        self._input_tokens += completion.input_tokens
        self._output_tokens += completion.output_tokens
        if self._emitter:
            self._emitter.model_response_received(
                self._spec.model.provider, completion.model, steps, completion.input_tokens, completion.output_tokens
            )

        assistant = AIMessage(
            content=completion.text,
            tool_calls=[
                {"name": call.name, "args": call.arguments, "id": call.id, "type": "tool_call"}
                for call in completion.tool_calls
            ],
        )
        return {"messages": [assistant], "steps": steps, "output": completion.text}

    async def _run_tools(self, state: AgentState) -> dict[str, Any]:
        assert self._spec is not None
        messages = state.get("messages", [])
        last = messages[-1] if messages else None
        if not isinstance(last, AIMessage):
            return {"messages": []}

        results: list[BaseMessage] = []
        for call in last.tool_calls:
            name = str(call.get("name", ""))
            if self._emitter:
                self._emitter.tool_invocation_started(name, int(state.get("steps", 0)))
            outcome = await self._tools.execute(name, dict(call.get("args", {})))
            if self._emitter:
                self._emitter.tool_invocation_completed(name, int(state.get("steps", 0)), outcome.ok)
            results.append(
                ToolMessage(
                    content=json.dumps(outcome.output if outcome.ok else {"error": outcome.error_code}, sort_keys=True),
                    tool_call_id=str(call.get("id", "")),
                    name=name,
                )
            )
        if int(state.get("steps", 0)) >= self._spec.limits.max_turns:
            return {"messages": results, "halted": True}
        return {"messages": results}

    def _checkpoint(self, final: dict[str, Any]) -> bytes | None:
        assert self._spec is not None
        if self._spec.checkpoint.mode == "none":
            return None
        state = {
            "messages": [message_to_dict(message) for message in final.get("messages", [])],
            "steps": int(final.get("steps", 0)),
        }
        return encode(Checkpoint(state=state, agent_version_id=self._spec_version_id))
