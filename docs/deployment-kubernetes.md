# Kubernetes deployment, autoscaling and observability

Phase 15 packages the platform for a Kubernetes cluster. The Helm chart is the primary production path. The
Kubernetes base is a self-contained development/proof deployment with PostgreSQL, NATS, Redis and MinIO
StatefulSets; production should use managed stateful services or an operator.

## 1. Build images

All images are multi-stage, non-root and versioned by `VERSION`, `COMMIT` and `BUILD_DATE`:

```bash
export VERSION=0.1.0 COMMIT=$(git rev-parse --short HEAD) BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
make images
make scan-images       # Trivy HIGH/CRITICAL gate; CI also runs this
```

| Image | Dockerfile |
|---|---|
| API | `infrastructure/docker/go-service.Dockerfile` (`SERVICE=api`) |
| Orchestrator | `infrastructure/docker/go-service.Dockerfile` (`SERVICE=orchestrator`) |
| Worker / Python runtime | `infrastructure/docker/worker.Dockerfile` |
| Dashboard | `infrastructure/docker/dashboard.Dockerfile` |
| CLI | `infrastructure/docker/cli.Dockerfile` |
| Migrations | `infrastructure/docker/migrate.Dockerfile` |

Go builds use `-trimpath -buildvcs=false -buildid=` and the module graph is frozen by `go.sum`. Dependency base
images are pinned by tag; digest pinning and a registry mirror should be added at release time.

## 2. Secrets

Create one external secret (External Secrets, Sealed Secrets, Vault or cloud secret manager) named by
`secrets.existingSecret` (default `agent-platform-secrets`) with these keys:

```text
API_DATABASE_URL
ORCHESTRATOR_DATABASE_URL
MIGRATE_CONTROL_DATABASE_URL
MIGRATE_EXECUTION_DATABASE_URL
NATS_URL
SECRETS_MASTER_KEY
SECRETS_ACTIVE_KEY_ID
SECRETS_PREVIOUS_KEYS
WORKER_CONTROL_TOKEN
INTERNAL_API_TOKEN
OIDC_ISSUER_URL
OIDC_CLIENT_ID
DASHBOARD_API_KEY
```

The worker receives only `NATS_URL` and `WORKER_CONTROL_TOKEN`; it never receives database URLs or the dashboard
key. Never store secret values in values files or workflow/agent JSON. See `docs/phase-14-security.md`.

## 3. Helm install

```bash
helm upgrade --install agent-platform infrastructure/helm/agent-platform \
  --namespace agent-platform --create-namespace \
  --set global.imagePullSecrets[0].name=registry-pull \
  --set images.api.repository=registry.example.com/agent-platform-api \
  --set images.api.tag=0.1.0 \
  --set images.orchestrator.repository=registry.example.com/agent-platform-orchestrator \
  --set images.orchestrator.tag=0.1.0 \
  --set images.worker.repository=registry.example.com/agent-platform-worker \
  --set images.worker.tag=0.1.0 \
  --set images.dashboard.repository=registry.example.com/agent-platform-dashboard \
  --set images.dashboard.tag=0.1.0 \
  --set images.migrate.repository=registry.example.com/agent-platform-migrate \
  --set images.migrate.tag=0.1.0 \
  -f my-production-values.yaml
```

The migration Job is a `pre-install,pre-upgrade` Helm hook and runs both control and execution migration paths.
The API and orchestrator replica sets use `maxUnavailable: 0`; the API and orchestrator have PDBs and topology
spread constraints.

Helm chart controls:

- namespace/release naming
- image repositories and tags
- replicas and resources
- worker KEDA min/max and thresholds
- PodDisruptionBudgets
- service accounts and image pull secrets
- NetworkPolicies
- orchestrator artifact PVC (`ReadWriteMany`; use object storage in production for stronger durability)
- ServiceMonitor and PrometheusRule
- Ingress/TLS

If KEDA is not installed, render with `--set worker.autoscaling.keda.enabled=false` and scale the Deployment
manually. If the Prometheus Operator CRDs are not installed, disable `monitoring.serviceMonitor.enabled` and
`monitoring.prometheusRule.enabled`.

## 4. Self-contained Kubernetes base

```bash
kubectl apply -f infrastructure/kubernetes/namespaces/platform.yaml
kubectl apply -f infrastructure/kubernetes/storage/infrastructure.yaml
kubectl apply -f infrastructure/kubernetes/jobs/migrate.yaml
kubectl apply -k infrastructure/kubernetes
```

The base includes example credentials with `REPLACE_ME`; create the real Secret before applying workloads. It
uses `ReadWriteMany` for orchestrator artifacts because two orchestrator replicas may be scheduled on different
nodes. A cluster without RWX storage should use an object-store-backed artifact path or schedule one replica.

For Helm, do not also apply the base workloads; use either the chart or the base.

## 5. Autoscaling

KEDA scales the worker Deployment, not the API, and uses queue signals rather than CPU:

- pending task depth
- oldest pending task age
- active worker utilization

The ScaledObject allows one idle worker and up to 20 by default. The orchestrator publishes queue metrics from
PostgreSQL; KEDA queries Prometheus, not a worker pod. The worker has no inbound task service, so scaling adds
capacity to the existing durable NATS consumers.

Provider-aware scheduling is enforced inside the orchestrator:

- `ORCHESTRATOR_PROVIDER_CONCURRENCY` caps in-flight tasks by provider.
- `ORCHESTRATOR_PROVIDER_RATE_PER_MINUTE` caps recent starts per provider per rolling minute.
- `ORCHESTRATOR_TOOL_CONCURRENCY` caps in-flight tasks by tool version.
- project active/queued quotas, tenant active-task caps and maximum execution duration still apply.

This prevents repeatedly dispatching work to a provider/key that will immediately rate-limit. Per-key quotas can
be layered by creating separate workers/pools or adding a provider-key label to the provider-rate query.

## 6. High availability

- API: 2+ replicas, no local state, rolling update `maxUnavailable: 0`, PDB.
- Orchestrator: 2+ replicas. Dispatch uses PostgreSQL row locks and `SKIP LOCKED`; the outbox publisher also
  claims with `SKIP LOCKED`. The reaper is safe to run concurrently because every transition is fenced. No
  in-memory scheduler ownership is required for correctness.
- NATS: 3-node JetStream StatefulSet (or managed NATS) with persistent volumes.
- PostgreSQL: managed primary/standby with PITR backups; the base single StatefulSet is not HA.
- MinIO/object storage: versioning and replication/backup policy; checkpoint artifacts are not a substitute for
  database backups.
- Redis: is optional cache infrastructure today; use a managed HA Redis or accept cold recovery. No execution
  correctness depends on Redis.
- Readiness/liveness: `/readyz` includes database and NATS checks; `/healthz` is liveness only.
- Graceful termination: worker `terminationGracePeriodSeconds: 45`, preStop delay and existing drain support.

## 7. OpenTelemetry

Set `OTEL_EXPORTER_OTLP_ENDPOINT` (and optionally `OTEL_TRACES_SAMPLER_ARG`) for API/orchestrator/worker and for
the Python runtime, which inherits the tracing configuration through the worker's safe environment allowlist.
Propagation is W3C Trace Context:

- API HTTP spans and orchestrator gRPC client spans
- worker control gRPC spans
- Python agent node, model and tool spans
- NATS publish/consume spans using the outbox traceparent header
- PostgreSQL query spans through the pool tracer

Traces are exported to the OTel collector and forwarded to the trace backend. Use slow-query/error logs for
identifiers rather than putting them into metric labels.

## 8. Metrics and dashboards

Every Go service exposes `/metrics` on its HTTP port; the worker uses port 8082. The metric families are listed in
`infrastructure/monitoring/README.md`. Important production queries:

```promql
max(agent_platform_queue_pending_tasks)
max(agent_platform_queue_oldest_pending_seconds)
sum(rate(agent_platform_http_requests_total{status="5xx"}[5m]))
histogram_quantile(0.95, sum by (le) (rate(agent_platform_http_request_duration_seconds_bucket[5m])))
sum(rate(agent_platform_worker_tasks_total{outcome="failed"}[5m]))
sum by (provider) (rate(agent_platform_ai_tokens_total[5m]))
```

Install kube-prometheus-stack, Loki, Promtail and the seven dashboards using
`infrastructure/monitoring/README.md`. The dashboard provider uses Grafana's sidecar and a labelled ConfigMap, so
dashboard JSON remains versioned in this repository.

## 9. Alerts

`infrastructure/kubernetes/monitoring/prometheusrule.yaml` and the Helm chart cover:

- API and orchestrator unavailable
- NATS disconnected
- queue backlog and queue age
- no healthy worker
- repeated lease expiration
- database connection exhaustion
- high execution failure rate
- provider errors
- persistent-volume capacity pressure

Route Alertmanager receivers to the on-call system; the provided values leave webhooks empty.

## 10. Operations checklist

Before production:

- Replace all `REPLACE_ME` values and use External Secrets.
- Use managed PostgreSQL/NATS/object storage or tested operators with backups.
- Enable TLS/mTLS for API-to-orchestrator and worker-to-orchestrator gRPC; terminate public TLS at ingress.
- Set `AGENT_MCP_ALLOW_STDIO=false` and `AGENT_MCP_ALLOW_PRIVATE_NETWORKS=false`.
- Set a real `SECRETS_MASTER_KEY` and key rotation plan.
- Run `helm lint`, `helm template`, `kubectl kustomize`, image scans and the full integration suite.
- Load-test KEDA from zero to max while a slow provider stalls tasks; verify it scales on queue age, not CPU.
- Exercise a PostgreSQL restore, NATS node loss, worker pod eviction and object-store outage.
