# Monitoring

Services emit structured JSON logs to stdout (Zap) with `service`, `environment` and `version` fields. OpenTelemetry
traces, metrics and dashboards are wired in Phase 03, when there are spans worth collecting beyond health checks.

Planned here:

- OTel collector config (OTLP receiver, batch processor, exporters).
- Prometheus scrape config and alert rules for readiness, scheduler lag, lease expiry rate and outbox depth.
- Grafana dashboards for execution latency and failure codes.

No component makes a correctness decision from monitoring data; see
[architecture.md §9.4](../../docs/architecture.md#94-where-execution-state-is-stored).
