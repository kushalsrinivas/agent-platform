# syntax=docker/dockerfile:1
# Migration image: the migrate CLI plus the immutable SQL tree used by the Kubernetes Job and Compose.
FROM migrate/migrate:v4.20.1
COPY migrations/control /migrations/control
COPY migrations/execution /migrations/execution
USER 65532:65532
ENTRYPOINT ["migrate"]
