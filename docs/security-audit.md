# Security audit — release candidate 0.1.0-rc.1

Date: 2026-11-24. Scope: the application, container definitions, Helm chart and production deployment assets.
The self-contained Kubernetes base is development-only and is called out separately.

## Result

**PASS for the release candidate**, with one accepted dev-only manifest finding and no known reachable
application vulnerability.

## Scans

| Scan | Scope | Result |
|---|---|---|
| `govulncheck ./...` | Go modules and symbols | 0 reachable vulnerabilities; 4 vulnerabilities exist in required modules but no vulnerable symbol is called |
| `npm audit --omit=dev --audit-level=high` | dashboard production dependencies | 0 vulnerabilities |
| `pip-audit` | Python runtime and SDK environments | 0 known vulnerabilities |
| `trivy config` | Helm chart and Dockerfiles | 0 HIGH/CRITICAL findings |
| `trivy fs --scanners secret` | repository secrets | 0 findings |
| `trivy image` | API, orchestrator, worker, dashboard, CLI, migrate (CI) | HIGH/CRITICAL gate in `.github/workflows/ci.yml` |
| `go vet` | Go packages | clean |

Reproduce with `./scripts/security-audit.sh`; the script is also a blocking CI job.

## Controls verified by the security suite

`tests/integration/security_flow_test.go` runs against the real stack and verifies:

- owner/admin/developer/viewer permission groups and least privilege for project-scoped API keys
- cross-tenant reads and mutations return not-found, including every tenant resource
- project-scope confinement for agents, workflows, executions and secrets
- Postgres row-level security with, without and across tenant context
- encrypted project secret lifecycle; plaintext never returns from reads
- append-only audit events and organisation discovery
- deployed Python worker isolation: non-root, read-only root, no added capabilities, no PostgreSQL route
- NATS authentication and worker/API service tokens in the hardened Compose stack

The chaos suite additionally verifies concurrent idempotent submission, provider-outage failure handling,
parallel cancellation and worker-pod restart recovery without duplicate successful commits.

## Accepted risk

The self-contained `infrastructure/kubernetes/` base has four HIGH findings (`KSV-0014`) because the
PostgreSQL, NATS, Redis and MinIO development StatefulSets do not set `readOnlyRootFilesystem`. These services
are not the production path; the Helm chart deploys against externally managed infrastructure and scans clean.
Root filesystems were left writable to keep the proof cluster bootable, and changing untested storage pods
without a cluster to validate them would add more risk than it removes.

## Residual risks

- The release-candidate benchmarks use the deterministic echo provider; a small real-provider test is still
  required before a production label.
- KEDA, Helm hooks and NetworkPolicies have been rendered/linted but not exercised against a live cluster.
- The base cluster's single-node data services are not HA and must not be used for production.
- Four module-level Go vulnerabilities are not reachable from this code; revisit when upstream fixes ship.

## Release decision

The static, dependency, image, secret and manifest gates pass. Security-critical application controls pass
against real infrastructure. Production promotion remains conditional on the live-cluster and real-provider
checks listed above.
