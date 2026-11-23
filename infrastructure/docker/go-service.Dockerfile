# syntax=docker/dockerfile:1

# One Dockerfile for all three Go deployables. SERVICE selects the cmd directory.
ARG SERVICE=api
ARG VERSION=dev
ARG COMMIT=unknown

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG SERVICE
ARG VERSION
ARG COMMIT
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Version=${VERSION} \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.Commit=${COMMIT} \
      -X github.com/agent-platform/agent-platform/internal/platform/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /out/service ./services/${SERVICE}/cmd/${SERVICE}

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -u 10001 -G app app

COPY --from=build /out/service /usr/local/bin/service

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/service"]
