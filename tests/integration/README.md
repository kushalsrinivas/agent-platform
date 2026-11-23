# Integration tests

Two suites run against the Compose data stores after migrations:

```bash
make integration-test
```

- `infrastructure_test.go` — schemas, role separation and JetStream availability.
- `schema_test.go` — RLS (with, without and cross tenant), org discovery, write/read role separation, idempotency
  uniqueness, lease/attempt constraints, terminal-state checks, version immutability, append-only events, closed
  attempt immutability, and outbox claim/mark/skip-locked behaviour.

Connection URLs default to `localhost:5432` and can be overridden with `API_DATABASE_URL`,
`ORCHESTRATOR_DATABASE_URL`, `POSTGRES_DATABASE_URL` and `ORCHESTRATOR_NATS_URL`. `make integration-test` builds
them from `POSTGRES_PORT` (read from `.env` when present, otherwise 5432).

The suite creates uniquely named organisations and cleans them up in dependency order. It needs no running API,
orchestrator or worker and no external network.
