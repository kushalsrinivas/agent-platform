# Monitoring and observability

The services expose Prometheus metrics at `/metrics` on their HTTP ports and emit OpenTelemetry traces/logs to
`OTEL_EXPORTER_OTLP_ENDPOINT`. Prometheus labels are bounded: route patterns, providers, pools, outcomes and
status classes. Execution and task IDs belong in traces and Loki logs, not metric labels.

## Kubernetes components

- `kubernetes/monitoring/otel-collector.yaml` — OTLP HTTP/gRPC collector, scrape bridge and Loki exporter.
- `kubernetes/monitoring/servicemonitor.yaml` — API, orchestrator and worker scrape endpoints.
- `kubernetes/monitoring/prometheusrule.yaml` — availability, backlog, lease, database, provider, failure and
  storage alerts.
- `loki/` — values for Grafana Loki and Promtail.
- `prometheus/values.yaml` — kube-prometheus-stack values, Grafana datasource/dashboard provisioning.
- `grafana/dashboards/` — seven provisioned dashboards:
  1. platform health
  2. orchestrator health
  3. worker health
  4. queue health
  5. provider performance
  6. database health
  7. execution reliability

Install the monitoring stack separately from the platform chart:

```bash
helm repo add grafana https://grafana.github.io/helm-charts
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm upgrade --install loki grafana/loki -n monitoring --create-namespace \
  -f infrastructure/monitoring/loki/loki-values.yaml
helm upgrade --install promtail grafana/promtail -n monitoring \
  -f infrastructure/monitoring/loki/promtail-values.yaml
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack -n monitoring \
  -f infrastructure/monitoring/prometheus/values.yaml
kubectl apply -f infrastructure/kubernetes/monitoring/
./infrastructure/monitoring/grafana/install-dashboards.sh
```

The dashboard JSON files are provisioned as a labelled ConfigMap for Grafana's sidecar; no dashboard content is
duplicated in Helm values.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to the collector service and keep `OTEL_TRACES_SAMPLER_ARG` at a production
appropriate rate. An unset endpoint deliberately leaves tracing no-op for local development.

## Metric families

| Family | Key signals |
|---|---|
| `agent_platform_http_*` | request count, p95 latency, 4xx/5xx classes, rate limiting |
| `agent_platform_queue_*` | pending tasks, oldest wait, active tasks |
| `agent_platform_orchestrator_*` | dispatches, retries, recoveries, lease expirations |
| `agent_platform_worker_*` | active tasks, slots, throughput, runtime errors |
| `agent_platform_ai_*` | input/output tokens, model latency, provider errors, tool calls |
| `agent_platform_db_*` | acquired/idle/total/max pool connections |
| `agent_platform_outbox_*` / `agent_platform_nats_connected` | delivery backlog and broker connectivity |

Estimated model cost is intentionally a dashboard/PromQL calculation from token counters rather than a hardcoded
price table in the services.
