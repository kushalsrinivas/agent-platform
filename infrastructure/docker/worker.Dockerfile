# syntax=docker/dockerfile:1

# The worker image carries the Go host and the Python runtime ABI in one image. The Go host owns NATS, leases and
# result reporting; the Python process it spawns per attempt owns only the agent execution.

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

ARG VERSION
ARG COMMIT
ARG BUILD_DATE

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Version=${VERSION} \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Commit=${COMMIT} \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /out/service ./services/worker/cmd/worker

FROM python:3.12-slim AS runtime

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.8.6 /uv /uvx /bin/

WORKDIR /app/runtime
COPY runtimes/python/pyproject.toml runtimes/python/uv.lock runtimes/python/README.md ./
RUN uv sync --frozen --no-dev --no-install-project
COPY runtimes/python/src ./src
RUN uv sync --frozen --no-dev
ENV PATH="/app/runtime/.venv/bin:$PATH"

COPY --from=build /out/service /usr/local/bin/service

RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid 10001 --create-home --shell /usr/sbin/nologin app
USER 10001:10001

EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/service"]
