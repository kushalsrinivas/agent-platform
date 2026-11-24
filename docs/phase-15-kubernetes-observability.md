# Phase 15 — Kubernetes, Autoscaling and Observability

Status: complete. The platform has a production-oriented Helm chart, a self-contained Kustomize base, KEDA
queue-based worker scaling, provider-aware dispatch quotas and a metrics/tracing/logging contract shared by Go,
Python and messaging boundaries.

## 1. Images

Multi-stage images are provided for the API, orchestrator, Python worker, dashboard, CLI and migration job. All
runtime images run non-root. Go builds use `-trimpath -buildvcs=false -buildid=` and receive `VERSION`, `COMMIT`
and `BUILD_DATE` build arguments. OCI labels carry image metadata. `make images` builds every image and
`make scan-images` runs Trivy with a HIGH/CRITICAL gate; CI builds and scans each image.

## 2. Kubernetes and Helm

`infrastructure/kubernetes/` is a Kustomize base with namespaces, self-contained PostgreSQL/NATS/Redis/MinIO
StatefulSets, migration Job, API/orchestrator/worker/dashboard Deployments, Services, service accounts, PDBs
(PDBs), NetworkPolicies, KEDA, ingress and monitoring resources. It is intended for a proof/development cluster.

`infrastructure/helm/agent-platform/` is the production chart. It parameterises images, replicas, resources,
environment, secret references, service accounts, NetworkPolicies, artifact persistence, KEDA thresholds,
ServiceMonitor/PrometheusRule and ingress/TLS. The migration Job is a Helm pre-install/pre-upgrade hook.

Secrets are never chart-created: an external secret named `agent-platform-secrets` supplies database URLs, NATS
credentials, the secret keyring, worker/API identities and OIDC settings. The worker receives only NATS and its
control token; it does not receive database or dashboard credentials.

## 3. Autoscaling and quotas

KEDA scales the Python worker Deployment from pending queue depth, oldest pending task age and worker utilization.
It never relies on CPU alone because a provider-stalled attempt can look idle to the CPU.

The scheduler already enforced active/queued execution quotas, per-tenant active-task caps, provider/tool
concurrency and execution duration. Phase 15 adds `ORCHESTRATOR_PROVIDER_RATE_PER_MINUTE`, a rolling one-minute
per-provider dispatch cap, so a rate-limited provider is not repeatedly assigned work. Keys and models remain
bounded by their respective worker/pool or provider quotas.

## 4. High availability

The API and orchestrator default to two replicas with rolling updates that never drop below capacity, PDBs and
topology spread. Multiple orchestrators are safe because every dispatch/reaper/outbox transition is fenced by
PostgreSQL row locks and `SKIP LOCKED`; there is no in-memory scheduler ownership assumption. NATS uses a 3-node
JetStream StatefulSet; production should use managed PostgreSQL with PITR, managed NATS/object storage or tested
operators. Redis is cache-only and is not on the execution correctness path.

## 5. OpenTelemetry

Go services initialise OTLP tracing from standard `OTEL_*` environment variables and propagate W3C Trace Context
through HTTP, gRPC, NATS outbox headers and the Python runtime context. Instrumentation includes HTTP, gRPC,
PostgreSQL queries, NATS publish/consume, agent node execution, model invocation and tool invocation. Python
spans use the same attempt traceparent, so worker and model/tool work appears under one trace. An unset OTLP
endpoint leaves tracing no-op locally.

## 6. Prometheus

All services expose `/metrics`. Metrics cover:

- API request counts, latency, error classes and rate limits
- queue depth/age, outbox depth/age, active tasks/executions
- dispatch, retry, recovery and lease-expiration counters
- worker active/available slots, throughput and runtime errors
- input/output tokens, model latency, provider errors and tool calls
- database pool acquired/idle/total/max
- NATS connectivity

Labels are bounded to service/method/route pattern/status class, pool, provider and outcome. Execution/task IDs
stay in traces and logs.

## 7. Logs and dashboards

Loki + Promtail values are provided under `infrastructure/monitoring/loki/`. Seven provisioned Grafana dashboards
cover platform, orchestrator, worker, queue, provider, database and execution reliability. The dashboards are
versioned JSON and installed by a labelled ConfigMap for Grafana's sidecar.

Prometheus alert rules cover API/orchestrator unavailability, NATS disconnection, queue backlog/age, unhealthy
worker fleet, lease expiration spikes, database connection exhaustion, execution failure rate, provider errors
and storage pressure. Alertmanager receivers are intentionally left for the on-call integration.

## 8. Verification

- `helm lint infrastructure/helm/agent-platform`
- `helm template ...` renders all platform resources
- `kubectl kustomize infrastructure/kubernetes` renders the base
- Go/Python unit and integration suites remain green (`make integration-test` 178.3s; Python 69 passed; Ruff and mypy clean)
- `/metrics` smoke tests on API (`agent_platform_http_*`), orchestrator (`queue`, `execution`, `db`, `nats`) and
  worker (`active_tasks`, `slots_total`)
- CI jobs for manifests, image build + Trivy scan and live integration

See [deployment-kubernetes.md](deployment-kubernetes.md) for install, secrets, upgrade and operational procedures.

## 9. Known limits

- The base cluster's single-node stateful services are not production HA.
- KEDA scaling requires Prometheus and KEDA CRDs; disable the ScaledObject when absent.
- The orchestrator artifact PVC uses `ReadWriteMany`; clusters without RWX storage should use object storage or
  constrain replicas. The current artifact backend is filesystem-backed.
- Provider quotas are provider-level; per-model-key quotas require separate provider labels or worker pools.
- Alertmanager receivers must be wired to a real incident system.
