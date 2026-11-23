# syntax=docker/dockerfile:1

# The Python runtime image. One image per runtime ABI; the worker records the image digest it executed.
FROM python:3.12-slim AS base

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.8.6 /uv /uvx /bin/

WORKDIR /app

COPY runtimes/python/pyproject.toml runtimes/python/uv.lock runtimes/python/README.md ./
RUN uv sync --frozen --no-dev --no-install-project

COPY runtimes/python/src ./src
RUN uv sync --frozen --no-dev

ENV PATH="/app/.venv/bin:$PATH"

RUN groupadd --gid 10001 runtime \
    && useradd --uid 10001 --gid 10001 --create-home runtime
USER 10001:10001

# The worker starts one process per attempt over a Unix socket (Phase 03). Until then the smoke entrypoint
# reports the runtime ABI so the image can be verified in CI.
ENTRYPOINT ["python", "-m", "agent_runtime"]
CMD ["version"]
