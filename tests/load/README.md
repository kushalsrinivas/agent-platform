# Load and chaos suites (deferred)

- **Load**: dispatch-to-accept latency, scheduler throughput, lease churn, SSE fan-out. Arrives with the execution
  lifecycle in Phase 03/04; k6 or a Go load generator, run against Compose or a staging deployment.
- **Chaos**: worker kill mid-attempt, NATS restart, orchestrator crash during commit, PostgreSQL failover. The
  expected behaviour is already specified in [failure-and-recovery.md](../../docs/failure-and-recovery.md); the suite
  proves it.

Neither suite exists yet. Naming them here prevents "we'll test it later" from becoming "we didn't notice".
