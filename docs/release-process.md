# Release process

## Versioning

- Tags are `vMAJOR.MINOR.PATCH`; release candidates append `-rc.N` (for example `v0.1.0-rc.1`).
- `MAJOR` covers incompatible API, event, runtime-ABI or checkpoint-schema changes.
- `MINOR` adds compatible capabilities. `PATCH` is fixes only.
- Image tags and the Helm chart version use the tag without the leading `v`.
- Migration versions are forward-only in production; every migration has a tested down file for local recovery.

## Pre-release gate

Run from a clean checkout:

```bash
make test                 # Go -race + Python runtime/SDK
make lint                 # gofmt, vet, golangci-lint, Ruff, mypy, dashboard
make manifests            # Helm lint/template + Kustomize render
make migrate-check        # apply every migration to a scratch database
make backup-verify        # dump, restore and compare core tables
make security-audit       # govulncheck, npm/pip audit, Trivy config/secret
make integration-test     # real HTTP, Postgres, NATS, workers, MCP, SDK
make chaos-test           # pod restart, parallel cancellation, provider outage, duplicates
make load-baseline        # smoke the workload generator
```

Attach `docs/performance-report.md` and `docs/security-audit.md` to the release decision. Do not tag with a
failing gate, an unverified migration, or an open Sev 1/Sev 2.

## Tag and publish

The release workflow `.github/workflows/release.yml` runs on a `v*` tag and calls the full CI workflow first.

```bash
git tag -s v0.1.0-rc.1 -m "Agent Platform 0.1.0-rc.1"
git push origin v0.1.0-rc.1
```

It then:

1. validates migrations and backup/restore on a scratch database;
2. builds all six images, scans HIGH/CRITICAL with Trivy and pushes version and commit tags to GHCR;
3. packages the Helm chart and pushes it to `oci://ghcr.io/<repo>/charts`;
4. extracts `CHANGELOG.md` for the version and creates the GitHub release with the chart attached.

Required repository permissions: `contents: write`, `packages: write`. GHCR packages must allow the workflow
token to publish; environments requiring approval must be configured before the first tag.

## Deploy

```bash
helm upgrade --install agent-platform oci://ghcr.io/<repo>/charts/agent-platform \
  --version 0.1.0-rc.1 -n agent-platform --create-namespace -f production-values.yaml
```

The migration Job is a pre-install/pre-upgrade hook. Confirm the release and a smoke execution:

```bash
helm -n agent-platform status agent-platform
kubectl -n agent-platform rollout status deploy/agent-platform-api
kubectl -n agent-platform rollout status deploy/agent-platform-orchestrator
```

## Rollback

```bash
helm -n agent-platform history agent-platform
helm -n agent-platform rollback agent-platform <previous-revision>
```

- Roll back the chart before rolling back the schema. Migrations are forward-only; if a migration must be
  reversed, restore the pre-upgrade backup (runbook 10 in `docs/runbooks.md`).
- Keep previous image digests; the chart pins them by tag.
- Verify readiness and one execution after rollback.

## Release candidate status

The repository contains the `0.1.0-rc.1` changelog entry, release workflow, packaged-chart target, runbooks and
verification evidence. The tag has **not** been created from this working tree. Create it only after the
pre-release gate passes on a clean commit and the user approves the release.
