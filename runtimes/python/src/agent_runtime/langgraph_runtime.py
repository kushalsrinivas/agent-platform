"""LangGraph adapter.

The graph is the unit of checkpointable state: one ``agent`` node calls the model, one ``tools`` node executes
bound platform tools, and a conditional edge loops until the model stops asking for tools or the turn budget is
exhausted. LangGraph-specific detail stops at this module; the runtime contract above it stays framework-agnostic.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
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

from agent_runtime.delegation import DelegationError, SupervisorClient
from agent_runtime.events import EventEmitter
from agent_runtime.runtime import AgentRuntime
from agent_runtime.spec import AgentVersionSpec, NodeContext, NodeResult
from checkpointing.codec import Checkpoint, CheckpointError, encode
from checkpointing.sink import CheckpointSink
from mcp.manager import MCPError, MCPManager
from model_providers.base import Completion, Message, ProviderConfig, ProviderError, ToolCall, ToolSpec
from model_providers.registry import get_provider
from tool_runtime import ToolRegistry, default_registry
from tool_runtime.policy import DEFAULT_POLICY, ToolExecutor, ToolInvocation, ToolPolicy

log = logging.getLogger(__name__)


class AgentState(TypedDict, total=False):
    """State carried between super-steps and checkpointed at node end."""

    messages: Annotated[list[BaseMessage], add_messages]
    steps: int
    output: str
    halted: bool
    phase: str


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
    payload: dict[str, Any] = {
        "input": input_data,
        "params": context.node_params,
        "node_input": context.node_input,
    }
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
        self._spec_attempt_id = ""
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
        self._sink: CheckpointSink | None = None
        self._restored_output: dict[str, Any] | None = None
        self._restored_metrics: dict[str, Any] = {}
        self._restored_phase = ""
        self._final_checkpoint_written = False
        self._tool_scope = ""
        self._executor = ToolExecutor()
        self._mcp: MCPManager | None = None
        self._supervisor: SupervisorClient | None = None

    # -- AgentRuntime contract -------------------------------------------------

    async def initialize(self, spec: AgentVersionSpec, context: NodeContext) -> None:
        self._spec = spec
        self._spec_version_id = context.agent_version_id
        self._spec_attempt_id = context.attempt_id
        self._tool_scope = f"{context.execution_id}:{context.task_id}:{context.node_id}"
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
        await self._start_tools(spec, context)
        self._tool_specs = self._build_tools(spec, context)
        self._graph = self._build_graph()
        self._sink = None
        if spec.checkpoint.mode != "none":
            self._sink = CheckpointSink.from_env(context.agent_version_id)
        if self._emitter:
            self._emitter.agent_initialized(context.agent_version_id, spec.model.provider, spec.model.name)

    async def execute(self, input_data: dict[str, Any], context: NodeContext) -> NodeResult:
        assert self._graph is not None and self._spec is not None
        self._input_tokens = 0
        self._output_tokens = 0
        self._deadline = context.deadline
        self._final_checkpoint_written = False

        if self._restored_output is not None:
            output = dict(self._restored_output)
            if os.environ.get("AGENT_RUNTIME_RESUME_MARKER") == "1":
                output["resumed"] = True
            if self._emitter:
                self._emitter.agent_completed(
                    int(self._restored_metrics.get("steps", 0)),
                    int(self._restored_metrics.get("input_tokens", 0)),
                    int(self._restored_metrics.get("output_tokens", 0)),
                )
            return NodeResult.success(
                output,
                input_tokens=int(self._restored_metrics.get("input_tokens", 0)),
                output_tokens=int(self._restored_metrics.get("output_tokens", 0)),
                steps=int(self._restored_metrics.get("steps", 0)),
                model=str(self._restored_metrics.get("model", self._provider_config.model)),
            )

        if self._restored_messages is not None:
            messages = list(self._restored_messages)
            steps = self._restored_steps
            phase = self._restored_phase
        else:
            messages = [SystemMessage(content=self._spec.instructions or "You are a helpful agent.")]
            messages.append(HumanMessage(content=_user_payload(input_data, context)))
            steps = 0
            phase = ""

        try:
            final = await self._graph.ainvoke(
                {"messages": messages, "steps": steps, "phase": phase},
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
        output = {"report": text, "steps": steps, "model": self._provider_config.model}
        state = {
            "messages": [message_to_dict(message) for message in final.get("messages", [])],
            "steps": steps,
            "completed": True,
            "output": output,
            "input_tokens": self._input_tokens,
            "output_tokens": self._output_tokens,
            "model": self._provider_config.model,
        }
        checkpoint = None
        if self._spec.checkpoint.mode != "none":
            checkpoint = encode(Checkpoint(state=state, agent_version_id=self._spec_version_id))
            if not self._final_checkpoint_written:
                self._write_checkpoint(state)
                self._final_checkpoint_written = True
        pause = float(os.environ.get("AGENT_RUNTIME_TEST_PAUSE_SECONDS", "0") or 0)
        if pause > 0:
            await asyncio.sleep(pause)
        if self._emitter:
            self._emitter.agent_completed(steps, self._input_tokens, self._output_tokens)
        return NodeResult.success(
            output,
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
        self._restored_phase = str(state.get("phase", ""))
        if state.get("completed") and isinstance(state.get("output"), dict):
            self._restored_output = dict(state["output"])
            self._restored_metrics = {
                "input_tokens": int(state.get("input_tokens", 0)),
                "output_tokens": int(state.get("output_tokens", 0)),
                "steps": int(state.get("steps", 0)),
                "model": str(state.get("model", "")),
            }

    async def cancel(self) -> None:
        self._cancelled.set()

    async def shutdown(self) -> None:
        if self._supervisor is not None:
            await self._supervisor.close()
            self._supervisor = None
        if self._mcp is not None:
            await self._mcp.close()
            self._mcp = None

    # -- internals --------------------------------------------------------------

    async def _start_tools(self, spec: AgentVersionSpec, context: NodeContext) -> None:
        self._executor = ToolExecutor()
        self._mcp = None
        self._supervisor = None
        if spec.mcp_servers:
            self._mcp = MCPManager(spec.mcp_servers, context.credentials)
            try:
                await self._mcp.start()
            except MCPError as exc:
                await self._mcp.close()
                self._mcp = None
                raise ProviderError("MCP_INIT_FAILED", exc.message, retryable=False) from exc
        if spec.orchestration is not None:
            if not context.lease_id:
                raise ProviderError(
                    "DELEGATION_UNAVAILABLE", "supervisor delegation requires an attempt lease", retryable=False
                )
            self._supervisor = SupervisorClient(context.task_id, context.attempt_id, context.lease_id)

    def _build_tools(self, spec: AgentVersionSpec, context: NodeContext) -> list[ToolSpec]:
        specs: list[ToolSpec] = []
        for binding in spec.tools:
            tool = self._tools.get(binding.tool_id)
            if tool is None:
                raise ProviderError("unknown_tool", f"tool {binding.tool_id!r} is not available in this runtime image")
            policy = binding.policy or DEFAULT_POLICY

            async def run_platform(arguments: dict[str, Any], idempotency_key: str, impl: Any = tool) -> dict[str, Any]:
                return dict(await impl.run(arguments, idempotency_key=idempotency_key))

            self._executor.register(
                tool.name,
                ToolInvocation(handler=run_platform, policy=policy),
            )
            specs.append(
                ToolSpec(
                    name=tool.name,
                    description=f"Platform tool {tool.name} v{tool.version}",
                    parameters={"type": "object"},
                )
            )
        if self._mcp is not None:
            for discovered in self._mcp.tools:
                name = f"mcp__{discovered.server}__{discovered.name}"
                policy = self._mcp.policy(discovered)

                async def run_mcp(
                    arguments: dict[str, Any],
                    _idempotency_key: str,
                    server: str = discovered.server,
                    tool_name: str = discovered.name,
                    scoped: ToolPolicy = policy,
                ) -> dict[str, Any]:
                    if scoped.auth_scope and self._mcp is not None and not self._mcp.credential_available(server):
                        raise MCPError("MCP_AUTH_REQUIRED", f"server {server!r} requires a credential")
                    assert self._mcp is not None
                    return dict(await self._mcp.call_tool(server, tool_name, arguments))

                self._executor.register(
                    name,
                    ToolInvocation(
                        handler=run_mcp,
                        policy=policy,
                        annotations=discovered.annotations,
                        network_required=True,
                    ),
                )
                specs.append(
                    ToolSpec(
                        name=name,
                        description=(f"MCP tool {discovered.name} on {discovered.server}: {discovered.description}")[
                            :2000
                        ],
                        parameters=discovered.input_schema or {"type": "object"},
                    )
                )
        if self._supervisor is not None:
            specs.extend(self._register_supervisor_tools())
        return specs

    def _register_supervisor_tools(self) -> list[ToolSpec]:
        assert self._supervisor is not None
        supervisor = self._supervisor

        async def list_agents(_arguments: dict[str, Any], _key: str) -> dict[str, Any]:
            return await supervisor.list_agents()

        async def delegate_task(arguments: dict[str, Any], idempotency_key: str) -> dict[str, Any]:
            agent_id = str(arguments.get("agent_id", "")).strip()
            if not agent_id:
                raise ValueError("agent_id is required")
            version = int(arguments.get("version", 1))
            task = arguments.get("task")
            if not isinstance(task, dict):
                raise ValueError("task must be an object")
            result = await supervisor.delegate(
                agent_id,
                version,
                task,
                context=arguments.get("context") if isinstance(arguments.get("context"), dict) else None,
                context_refs=[str(item) for item in arguments.get("context_refs", []) if str(item)],
                output_schema=arguments.get("output_schema")
                if isinstance(arguments.get("output_schema"), dict)
                else None,
                timeout_seconds=int(arguments.get("timeout_seconds", 0) or 0),
                max_retries=int(arguments.get("max_retries", 0) or 0),
                idempotency_key=idempotency_key,
            )
            if bool(arguments.get("wait", False)):
                return await supervisor.wait(result["delegation_id"], float(arguments.get("wait_seconds", 300) or 300))
            return result

        async def delegate_tasks(arguments: dict[str, Any], idempotency_key: str) -> dict[str, Any]:
            tasks = arguments.get("tasks")
            if not isinstance(tasks, list) or not tasks:
                raise ValueError("tasks must be a non-empty array")
            if len(tasks) > 20:
                raise ValueError("at most 20 tasks may be delegated in one call")
            wait = bool(arguments.get("wait", True))
            wait_seconds = float(arguments.get("wait_seconds", 300) or 300)
            fallback_agent = str(arguments.get("fallback_agent_id", "")).strip()
            fallback_version = int(arguments.get("fallback_version", 1) or 1)

            async def start(index: int, item: Any) -> dict[str, Any]:
                if not isinstance(item, dict) or not str(item.get("agent_id", "")).strip():
                    return {
                        "status": "failed",
                        "error_code": "INVALID_ARGUMENT",
                        "error_message": "each task needs agent_id and task",
                        "agent_id": "",
                        "version": 0,
                        "delegation_id": "",
                        "child_execution_id": "",
                        "attempt": 1,
                    }
                agent_id = str(item["agent_id"])
                raw_task = item.get("task")
                task_payload: dict[str, Any] = raw_task if isinstance(raw_task, dict) else {}
                try:
                    created = await supervisor.delegate(
                        agent_id,
                        int(item.get("version", 1)),
                        task_payload,
                        context=item.get("context") if isinstance(item.get("context"), dict) else None,
                        context_refs=[str(value) for value in item.get("context_refs", []) if str(value)],
                        timeout_seconds=int(item.get("timeout_seconds", 0) or 0),
                        max_retries=int(item.get("max_retries", 0) or 0),
                        idempotency_key=f"{idempotency_key}:{index}",
                    )
                except DelegationError as exc:
                    return {
                        "status": "failed",
                        "error_code": exc.code,
                        "error_message": exc.message,
                        "agent_id": agent_id,
                        "version": int(item.get("version", 1)),
                        "delegation_id": "",
                        "child_execution_id": "",
                        "attempt": 1,
                    }
                return {**created, "agent_id": agent_id, "version": int(item.get("version", 1)), "attempt": 1}

            results = list(await asyncio.gather(*(start(index, item) for index, item in enumerate(tasks))))
            if wait:
                pending = [result for result in results if result.get("delegation_id")]
                waited = await asyncio.gather(
                    *(supervisor.wait(result["delegation_id"], wait_seconds) for result in pending),
                    return_exceptions=True,
                )
                for result, value in zip(pending, waited, strict=True):
                    if isinstance(value, Exception):
                        result.update(
                            {"status": "failed", "error_code": "DELEGATION_WAIT_FAILED", "error_message": str(value)}
                        )
                    elif isinstance(value, dict):
                        result.update(value)
            failed = [result for result in results if result.get("status") not in ("pending", "running", "succeeded")]
            recovered = 0
            if fallback_agent:

                async def recover(index: int, item: dict[str, Any]) -> None:
                    nonlocal recovered
                    raw_original = tasks[index].get("task") if isinstance(tasks[index], dict) else {}
                    original_task: dict[str, Any] = raw_original if isinstance(raw_original, dict) else {}
                    try:
                        created = await supervisor.delegate(
                            fallback_agent,
                            fallback_version,
                            original_task,
                            context=tasks[index].get("context")
                            if isinstance(tasks[index], dict) and isinstance(tasks[index].get("context"), dict)
                            else None,
                            idempotency_key=f"{idempotency_key}:fallback:{index}",
                        )
                        final = await supervisor.wait(created["delegation_id"], wait_seconds)
                        if final.get("status") == "succeeded":
                            item.update(
                                {
                                    "status": "succeeded",
                                    "recovered": True,
                                    "fallback_agent_id": fallback_agent,
                                    "fallback_delegation_id": created.get("delegation_id", ""),
                                    "output": final.get("output", {}),
                                    "error_code": "",
                                    "error_message": "",
                                }
                            )
                            recovered += 1
                    except DelegationError as exc:
                        item.update({"error_code": exc.code, "error_message": exc.message})

                await asyncio.gather(*(recover(index, item) for index, item in enumerate(results) if item in failed))
            succeeded = sum(1 for result in results if result.get("status") == "succeeded")
            return {
                "results": results,
                "succeeded": succeeded,
                "failed": len(results) - succeeded,
                "recovered": recovered,
            }

        async def get_delegation(arguments: dict[str, Any], _key: str) -> dict[str, Any]:
            delegation_id = str(arguments.get("delegation_id", "")).strip()
            if not delegation_id:
                raise ValueError("delegation_id is required")
            return await supervisor.get(delegation_id)

        async def publish_event(arguments: dict[str, Any], _key: str) -> dict[str, Any]:
            topic = str(arguments.get("topic", "")).strip()
            if not topic:
                raise ValueError("topic is required")
            raw_payload = arguments.get("payload")
            payload: dict[str, Any] = raw_payload if isinstance(raw_payload, dict) else {}
            return {"event_id": await supervisor.publish(topic, payload), "topic": topic}

        async def wait_event(arguments: dict[str, Any], _key: str) -> dict[str, Any]:
            topic = str(arguments.get("topic", "")).strip()
            if not topic:
                raise ValueError("topic is required")
            return await supervisor.wait_event(topic, int(arguments.get("timeout_seconds", 30) or 30))

        tools = [
            ToolSpec(
                name="list_delegation_agents",
                description="List the agents this supervisor may delegate to and the remaining delegation budget.",
                parameters={"type": "object", "properties": {}},
            ),
            ToolSpec(
                name="delegate_task",
                description=(
                    "Start a child execution for one allowed agent. Returns a delegation id; set wait=true to "
                    "block until the child finishes and receive its output."
                ),
                parameters={
                    "type": "object",
                    "required": ["agent_id", "task"],
                    "properties": {
                        "agent_id": {"type": "string"},
                        "version": {"type": "integer", "minimum": 1},
                        "task": {"type": "object"},
                        "context": {"type": "object"},
                        "context_refs": {"type": "array", "items": {"type": "string"}},
                        "output_schema": {"type": "object"},
                        "timeout_seconds": {"type": "integer", "minimum": 1},
                        "max_retries": {"type": "integer", "minimum": 0},
                        "wait": {"type": "boolean"},
                        "wait_seconds": {"type": "number", "minimum": 1},
                    },
                },
            ),
            ToolSpec(
                name="delegate_tasks",
                description=(
                    "Start several child executions concurrently, wait for all of them and aggregate the results. "
                    "A fallback agent may retry failed children once."
                ),
                parameters={
                    "type": "object",
                    "required": ["tasks"],
                    "properties": {
                        "tasks": {
                            "type": "array",
                            "minItems": 1,
                            "maxItems": 20,
                            "items": {
                                "type": "object",
                                "required": ["agent_id", "task"],
                                "properties": {
                                    "agent_id": {"type": "string"},
                                    "version": {"type": "integer", "minimum": 1},
                                    "task": {"type": "object"},
                                    "context": {"type": "object"},
                                    "context_refs": {"type": "array", "items": {"type": "string"}},
                                    "timeout_seconds": {"type": "integer", "minimum": 1},
                                    "max_retries": {"type": "integer", "minimum": 0},
                                },
                            },
                        },
                        "wait": {"type": "boolean"},
                        "wait_seconds": {"type": "number", "minimum": 1},
                        "fallback_agent_id": {"type": "string"},
                        "fallback_version": {"type": "integer", "minimum": 1},
                    },
                },
            ),
            ToolSpec(
                name="get_delegation",
                description="Read the status and output of a delegation previously started by this supervisor.",
                parameters={
                    "type": "object",
                    "required": ["delegation_id"],
                    "properties": {"delegation_id": {"type": "string"}},
                },
            ),
            ToolSpec(
                name="publish_agent_event",
                description="Publish a durable message to other agents in this execution hierarchy.",
                parameters={
                    "type": "object",
                    "required": ["topic"],
                    "properties": {"topic": {"type": "string"}, "payload": {"type": "object"}},
                },
            ),
            ToolSpec(
                name="wait_agent_event",
                description="Wait for the next durable message on a topic from another agent.",
                parameters={
                    "type": "object",
                    "required": ["topic"],
                    "properties": {
                        "topic": {"type": "string"},
                        "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 600},
                    },
                },
            ),
        ]
        handlers = {
            "list_delegation_agents": list_agents,
            "delegate_task": delegate_task,
            "delegate_tasks": delegate_tasks,
            "get_delegation": get_delegation,
            "publish_agent_event": publish_event,
            "wait_agent_event": wait_event,
        }
        for spec in tools:
            self._executor.register(
                spec.name,
                ToolInvocation(handler=handlers[spec.name], policy=ToolPolicy(timeout_seconds=300)),
            )
        return tools

    def _build_graph(self) -> CompiledStateGraph[AgentState, None, AgentState, AgentState]:
        graph = StateGraph(AgentState)
        graph.add_node("agent", self._call_model)
        graph.add_node("tools", self._run_tools)
        graph.add_conditional_edges(START, self._entry, {"agent": "agent", "tools": "tools"})
        graph.add_conditional_edges("agent", self._route_after_agent, {"tools": "tools", "end": END})
        graph.add_conditional_edges("tools", self._route_after_tools, {"agent": "agent", "end": END})
        return graph.compile()

    def _entry(self, state: AgentState) -> str:
        """Resume a checkpoint taken after a model response directly at the tool node."""
        return "tools" if state.get("phase") == "tools_pending" else "agent"

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
        delay = float(os.environ.get("AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS", "0") or 0)
        if delay > 0 and steps == 1:
            await asyncio.sleep(delay)
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
        if completion.tool_calls:
            self._write_checkpoint(
                {
                    "messages": [message_to_dict(message) for message in [*state.get("messages", []), assistant]],
                    "steps": steps,
                    "phase": "tools_pending",
                    "completed": False,
                }
            )
            return {"messages": [assistant], "steps": steps, "output": completion.text, "phase": "tools_pending"}

        # The model produced the final answer. Persist it immediately: this is the "after model response, before
        # result commit" boundary, and a retry must not call the model again.
        final_messages = [*state.get("messages", []), assistant]
        output = completion.text
        self._write_checkpoint(
            {
                "messages": [message_to_dict(message) for message in final_messages],
                "steps": steps,
                "phase": "completed",
                "completed": True,
                "output": {"report": output, "steps": steps, "model": self._provider_config.model},
                "input_tokens": self._input_tokens,
                "output_tokens": self._output_tokens,
                "model": self._provider_config.model,
            }
        )
        self._final_checkpoint_written = True
        return {"messages": [assistant], "steps": steps, "output": output, "phase": "completed"}

    async def _run_tools(self, state: AgentState) -> dict[str, Any]:
        assert self._spec is not None
        messages = state.get("messages", [])
        last = messages[-1] if messages else None
        if not isinstance(last, AIMessage):
            return {"messages": []}

        calls = [call for call in last.tool_calls]
        if self._emitter:
            for call in calls:
                self._emitter.tool_invocation_started(str(call.get("name", "")), int(state.get("steps", 0)))
        outcomes = await asyncio.gather(
            *(
                self._executor.execute(
                    str(call.get("name", "")),
                    dict(call.get("args", {})),
                    idempotency_key=self._tool_idempotency_key(str(call.get("id", ""))),
                )
                for call in calls
            )
        )
        results: list[BaseMessage] = []
        for call, outcome in zip(calls, outcomes, strict=True):
            name = str(call.get("name", ""))
            if self._emitter:
                self._emitter.tool_invocation_completed(name, int(state.get("steps", 0)), outcome.ok)
            call_id = str(call.get("id", ""))
            output = (
                outcome.output
                if outcome.ok
                else {
                    "error": outcome.error_code,
                    "message": outcome.error_message,
                }
            )
            results.append(
                ToolMessage(
                    content=json.dumps(output, sort_keys=True),
                    tool_call_id=call_id,
                    name=name,
                )
            )
            # A recoverable tool operation is a safe boundary. Persisting after each call bounds a crash to the
            # one external effect whose idempotency key the tool already received.
            self._write_checkpoint(
                {
                    "messages": [message_to_dict(message) for message in [*messages, *results]],
                    "steps": int(state.get("steps", 0)),
                    "phase": "tools_done",
                    "completed": False,
                }
            )
            tool_pause = float(os.environ.get("AGENT_RUNTIME_TEST_PAUSE_AFTER_TOOL_SECONDS", "0") or 0)
            if tool_pause > 0:
                await asyncio.sleep(tool_pause)
        if int(state.get("steps", 0)) >= self._spec.limits.max_turns:
            return {"messages": results, "halted": True, "phase": "tools_done"}
        return {"messages": results, "phase": "tools_done"}

    def _tool_idempotency_key(self, call_id: str) -> str:
        """Stable across attempts: a replayed tool call must deduplicate on this key."""
        return f"{self._tool_scope}:{call_id}" if self._tool_scope else call_id

    def _write_checkpoint(self, state: dict[str, Any]) -> None:
        if self._sink is None:
            return
        assert self._spec is not None
        try:
            self._sink.write(state)
        except CheckpointError:
            if self._spec.checkpoint.require_checkpoint:
                raise
            log.warning("checkpoint write failed; continuing because require_checkpoint is false")

    def _checkpoint(self, final: dict[str, Any]) -> bytes | None:
        assert self._spec is not None
        if self._spec.checkpoint.mode == "none":
            return None
        state = {
            "messages": [message_to_dict(message) for message in final.get("messages", [])],
            "steps": int(final.get("steps", 0)),
        }
        return encode(Checkpoint(state=state, agent_version_id=self._spec_version_id))
