# Load testing

Two drivers:

- `cmd/loadgen` submits real executions through the public API and reads completion/latency from PostgreSQL.
- `loadtest/k6/api.js` drives read-heavy public API traffic with k6.

Both default to the deterministic echo path. `loadgen` can use a real provider with `-provider`,
`-base-url`, `-model` and `-credential`.

## Stack setup

```bash
docker compose up -d --build postgres nats redis minio minio-init api orchestrator worker
make load-stack        # API limits, quotas, dispatch tuning, Python worker and echo worker
```

`make load-stack` starts the `worker-echo` Compose profile (64 slots) for orchestration benchmarks and keeps
the Python worker on the `default` pool. The echo worker uses the Go offline executor, not the Python runtime.

## Execution stages

```bash
make load-baseline        # 100
make load-moderate        # 500
make load-target          # 1,000
make load-queue-pressure  # 5,000 submissions, allow-open
make load-burst           # 10,000 submissions, allow-open
make load-runtime         # 100 through the real Python runtime
```

Results are written to `loadtest/results/*.json`. Queue-pressure and burst stages intentionally allow open
executions at the timeout; that is the point of the test.

For a sustained run, repeat the target command for several hours, for example:

```bash
for _ in $(seq 1 24); do
  bin/loadgen -api "$API_URL" -db "$POSTGRES_DATABASE_URL" -pool loadtest \
    -count 1000 -concurrency 32 -timeout 20m -report "loadtest/results/sustained-$(date +%H%M).json" || break
done
```

## API stages

```bash
k6 run loadtest/k6/api.js
VUS=100 RAMP=20s STEADY=60s API_URL=http://localhost:18080 k6 run loadtest/k6/api.js
```

Default thresholds: error rate < 2%, P95 < 500 ms, P99 < 1.5 s. The script creates its own organisation,
project, agent and workflow; it does not clean them up.

## Cleanup and quotas

- `loadgen` deletes its organisation and executions by default and repairs worker slot counters. Use
  `-cleanup=false` to keep a run for inspection.
- The API rate limit must be raised for load runs; `make load-stack` sets 100,000/minute and a 5,000 burst.
- Quotas are raised to 5,000 active and 20,000 queued in the load stack. The defaults remain 20/100.
- Do not run 5,000+ stages against a shared development database without checking disk and worker capacity.

See [../docs/performance-report.md](../docs/performance-report.md) for the measured baseline and bottleneck
analysis.
