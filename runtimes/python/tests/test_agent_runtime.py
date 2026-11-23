from agent_runtime import EchoAgentRuntime, build_echo_graph, run_echo_graph
from agent_runtime.langgraph_agent import run_echo_graph as _run
from agent_runtime.spec import AgentSpec, NodeContext


async def test_echo_graph_compiles_and_runs() -> None:
    result = await run_echo_graph("hello")
    assert result["output"] == "hello"
    assert result["steps"] == 1
    assert build_echo_graph() is not None
    assert _run is run_echo_graph


async def test_echo_runtime_returns_result() -> None:
    runtime = EchoAgentRuntime()
    spec = AgentSpec(name="echo")
    context = NodeContext(
        execution_id="exec_1",
        task_id="task_1",
        attempt_id="att_1",
        node_id="node_1",
        agent_version_id="agtv_1",
        execution_input={"input": "from-execution"},
    )

    result = await runtime.execute_node(spec, context)
    assert result.outcome == "succeeded"
    assert result.output["echo"] == "from-execution"
    assert result.output["agent"] == "echo"
