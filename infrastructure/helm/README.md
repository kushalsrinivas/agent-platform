# Helm chart

`agent-platform/` deploys the API, orchestrator, Python workers, dashboard, migration hook, KEDA ScaledObject,
PDBs, NetworkPolicies, service accounts and optional ServiceMonitor/PrometheusRule resources.

```bash
helm lint infrastructure/helm/agent-platform
helm template agent-platform infrastructure/helm/agent-platform --namespace agent-platform
helm upgrade --install agent-platform infrastructure/helm/agent-platform \
  --namespace agent-platform --create-namespace -f my-values.yaml
```

The chart does not create secrets. Provide an existing `agent-platform-secrets` with the keys documented in
[docs/deployment-kubernetes.md](../../docs/deployment-kubernetes.md).
