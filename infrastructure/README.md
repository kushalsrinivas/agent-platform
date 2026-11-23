# Infrastructure assets

| Directory | Contents |
|---|---|
| `docker/` | Dockerfiles for the Go services (one shared file), the Python runtime and the dashboard. |
| `compose/` | Reserved for per-environment Compose overrides. The canonical local stack is the root `docker-compose.yml`. |
| `kubernetes/` | Deferred with Kubernetes itself; see [ADR 0012](../docs/adr/0012-deployment-topology.md). |
| `helm/` | Deferred with Kubernetes. |
| `monitoring/` | Reserved for the OTel collector and dashboards. Services emit structured logs today; traces and metrics are wired in Phase 03. |

Images are pinned by tag. The object-storage service uses `pgsty/silo` because official MinIO images were
withdrawn in September 2026; see [development.md](../docs/development.md#object-storage).
