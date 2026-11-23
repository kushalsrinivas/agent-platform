# Shared contracts

Canonical wire contracts live in [`docs/api`](../../docs/api) (OpenAPI + envelope schema) and
[`proto/`](../../proto). Generated clients live in [`sdk/go`](../../sdk/go) and [`sdk/python`](../../sdk/python).

Nothing is copied here yet. This directory is for contract artefacts that must be shared without being owned by
one deployable — for example, generated TypeScript API types consumed by the dashboard. When the first API
resource is implemented in Phase 03, add the generator here rather than hand-maintaining a second copy.
