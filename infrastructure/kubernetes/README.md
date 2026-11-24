# Kubernetes base manifests

Self-contained base for a development/proof cluster. The production path is the Helm chart in
[`../helm/agent-platform`](../helm/agent-platform); the base includes single-node PostgreSQL, NATS, Redis and MinIO
StatefulSets so the platform can be exercised without managed dependencies.

```bash
kubectl apply -f infrastructure/kubernetes/namespaces/platform.yaml
kubectl apply -f infrastructure/kubernetes/storage/infrastructure.yaml
kubectl apply -f infrastructure/kubernetes/jobs/migrate.yaml
kubectl apply -k infrastructure/kubernetes
```

Replace image placeholders and `REPLACE_ME` secrets first. KEDA, Prometheus Operator and an ingress controller are
cluster prerequisites; disable the corresponding resources if they are absent. See
[docs/deployment-kubernetes.md](../../docs/deployment-kubernetes.md) for production guidance.
