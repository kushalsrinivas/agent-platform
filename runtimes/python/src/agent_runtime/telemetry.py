"""OpenTelemetry setup for the runtime process.

The Go worker passes the W3C traceparent for the attempt into the runtime context. Python spans therefore join the
same trace without the runtime needing NATS or database credentials.
"""

from __future__ import annotations

import os
from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.propagate import extract
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

_configured = False


def configure(service_name: str = "agent-runtime") -> None:
    global _configured
    if _configured:
        return
    _configured = True
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT") or os.getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    if not endpoint or os.getenv("OTEL_SDK_DISABLED", "").lower() == "true":
        return
    provider = TracerProvider(
        resource=Resource.create(
            {
                "service.name": service_name,
                "service.namespace": "agent-platform",
                "service.version": "0.1.0",
            }
        )
    )
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)


@contextmanager
def span(name: str, traceparent: str | None, attributes: dict[str, Any] | None = None) -> Iterator[trace.Span]:
    parent = extract({"traceparent": traceparent}) if traceparent else None
    with trace.get_tracer("agent-runtime").start_as_current_span(
        name,
        context=parent,
        attributes=attributes or {},
    ) as current:
        yield current


def shutdown() -> None:
    provider = trace.get_tracer_provider()
    flush = getattr(provider, "force_flush", None)
    if callable(flush):
        flush(timeout_millis=5000)
