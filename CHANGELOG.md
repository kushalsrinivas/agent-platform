# Changelog

All notable changes to the Agent Platform are recorded here. Versions follow [Semantic Versioning](https://semver.org/):
`MAJOR` for incompatible API/runtime changes, `MINOR` for compatible capabilities, `PATCH` for fixes.

## [0.1.0-rc.1] - 2026-11-24

First production-readiness release candidate.

### Added

- Durable execution engine: executions, tasks, attempts, leases, retries, checkpoints, child executions,
  delegations, approvals, delays, conditions, joins and cancellation.
- Multi-tenant control plane with organisations, projects, environments, agents, workflows, API keys, RBAC,
  project-scope isolation, row-level security and append-only audit events.
- Python agent runtime with LangGraph execution, deterministic echo provider, OpenAI-compatible, Anthropic and
  Gemini adapters, tool runtime, MCP clients and checkpoint serialisation.
- NATS JetStream dispatch, dead-letter redrive, outbox publishing and worker task delivery.
- Developer API, Python SDK, CLI (`agentctl`, `platformctl`), live dashboard and visual workflow builder.
- Encrypted project secrets, service identities, network segmentation and hardened worker/runtime isolation.
- Kubernetes base manifests, Helm chart, KEDA queue-based worker autoscaling and provider-aware dispatch quotas.
- OpenTelemetry traces across HTTP, gRPC, NATS, PostgreSQL and Python; Prometheus metrics; Loki/Promtail;
  seven Grafana dashboards; alert rules and operational runbooks.
- Phase 16 verification: integration suite covering eight end-to-end workflow shapes, chaos suite
  (worker pod restart, parallel cancellation, provider outage, duplicate submission), k6 API load test and a
  deterministic execution load generator with a measured performance report.
- Release engineering: tag-driven GHCR images, image scanning, Helm OCI packaging, migration validation,
  backup verification, release notes and a deployment rollback procedure.

### Security

- AES-256-GCM secret keyring with rotation, encrypted-at-rest secret values and runtime-only resolution.
- NATS authentication, worker/API service tokens, non-root containers, read-only root filesystems, capability
  drops, `prlimit` and deny-by-default MCP stdio/private-network access outside development.
- Image, dependency, secret and Kubernetes/Helm misconfiguration scans gate the release.

### Known limits

- The self-contained Kubernetes base is not production HA; use managed PostgreSQL, NATS and object storage.
- The default filesystem artifact backend is single-cluster; use object storage for cross-cluster recovery.
- Provider quotas are per provider, not per API key; use separate pools or labels for per-key isolation.

## [0.1.0-rc.0] - 2026-11-18

Phase 15 deployment and observability work was developed on this line and is included in `0.1.0-rc.1`.
