import logging

from agent_runtime import configure_logging
from agent_runtime.logging import JsonFormatter


def test_json_formatter_includes_extras() -> None:
    record = logging.LogRecord(
        name="agent_runtime.runtime",
        level=logging.INFO,
        pathname=__file__,
        lineno=1,
        msg="node executed",
        args=(),
        exc_info=None,
    )
    record.execution_id = "exec_1"

    output = JsonFormatter("agent-runtime").format(record)

    assert '"service":"agent-runtime"' in output
    assert '"execution_id":"exec_1"' in output
    assert '"message":"node executed"' in output


def test_configure_logging_is_idempotent() -> None:
    configure_logging("test", "debug")
    configure_logging("test", "debug")
    assert len(logging.getLogger().handlers) == 1
