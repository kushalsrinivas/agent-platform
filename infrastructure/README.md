# Infrastructure assets

| Directory | Contents |
|---|---|
| `docker/` | Multi-stage Dockerfiles for the Go services, Python worker, dashboard, CLI and migration job. |
| `compose/` | Reserved for per-environment Compose overrides. The canonical local stack is the root `docker-compose.yml`. |
| `kubernetes/` | Kustomize base, self-contained stateful services, KEDA, network policies and monitoring resources. |
| `helm/agent-platform/` | Production Helm chart with values for images, resources, secrets, autoscaling and monitoring. |
| `monitoring/` | OpenTelemetry collector, Prometheus/Loki values, seven Grafana dashboards and alert rules. |

Production deployment is documented in [docs/deployment-kubernetes.md](../docs/deployment-kubernetes.md). Local
Compose remains documented in [docs/development.md](../docs/development.md).
