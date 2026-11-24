from tool_runtime import default_registry


async def test_builtin_tools() -> None:
    registry = default_registry()
    assert registry.versions() == {"echo": "1", "add": "1", "tool_web_search": "1", "tool_news_fetch": "1"}

    echo = await registry.execute("echo", {"value": 42})
    assert echo.ok
    assert echo.output == {"echo": {"value": 42}}

    add = await registry.execute("add", {"numbers": [1, 2, 3.5]})
    assert add.ok
    assert add.output == {"sum": 6.5}

    search = await registry.execute("tool_web_search", {"query": "durable execution"})
    assert search.ok
    assert search.output["query"] == "durable execution"
    assert search.output["results"]


async def test_rejects_unknown_tool_and_bad_arguments() -> None:
    registry = default_registry()

    missing = await registry.execute("nope", {})
    assert not missing.ok
    assert missing.error_code == "unknown_tool"

    invalid = await registry.execute("add", {"numbers": ["one"]})
    assert not invalid.ok
    assert invalid.error_code == "invalid_arguments"


def test_duplicate_registration_fails() -> None:
    from tool_runtime import AddTool, ToolRegistry

    registry = ToolRegistry()
    registry.register(AddTool())
    try:
        registry.register(AddTool())
    except ValueError:
        pass
    else:
        raise AssertionError("expected ValueError")
