# Kubernetes (deferred)

Kubernetes is deliberately out of scope for Phase 1; see
[ADR 0012](../../docs/adr/0012-deployment-topology.md). The binaries are configuration-driven and container-ready,
so the manifests added here later are packaging, not code changes.

Prerequisites before this directory is populated: a real ingress/auth story, per-service resource limits, secret
delivery, and a migration job that runs before new replicas start.
