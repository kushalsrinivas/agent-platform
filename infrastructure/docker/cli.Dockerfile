# syntax=docker/dockerfile:1
# Developer CLI image. `docker run --rm -e AGENT_PLATFORM_* image ...` is usable without a host Go toolchain.

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
    CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= \
        -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Version=${VERSION} \
        -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Commit=${COMMIT} \
        -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/agentctl ./cli/cmd/agentctl

FROM alpine:3.22
ARG VERSION=dev
LABEL org.opencontainers.image.title="agent-platform-cli" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/kushalsrinivas/agent-platform"
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app
COPY --from=build /out/agentctl /usr/local/bin/agentctl
USER 10001:10001
ENTRYPOINT ["agentctl"]
