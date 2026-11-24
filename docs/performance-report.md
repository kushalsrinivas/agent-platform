# Performance and load report

Measured 2026-11-24 on the local Compose stack. This is a release-candidate baseline, not a production capacity
certificate: it runs on a laptop with a shared Docker VM and a deterministic executor.

## Environment

| Component | Value |
|---|---|
| Host | Apple M1, 8 CPU cores, 8 GB RAM, macOS |
| Container budget | 3.83 GiB visible to Compose services |
| PostgreSQL | 16.16, single node |
| NATS | 2.15.3, JetStream, single node |
| Orchestrator | 1 replica |
| Echo worker | 1 replica, 64 task slots |
| Python worker | 1 replica, 8 task slots, real LangGraph runtime |
| Model | deterministic echo provider; no external model calls |

The echo worker is a Compose `loadtest` profile service that uses the Go worker protocol with the offline echo
executor. It isolates orchestration/Postgres/NATS throughput from Python runtime startup and model latency. The
Python worker result is reported separately.

## Public API (k6)

`loadtest/k6/api.js` exercises the execution list, execution get and execution create endpoints.

| Run | VUs | Requests | Throughput | P50 | P95 | P99 | Errors |
|---|---|---|---:|---:|---:|---:|---:|
| Smoke | 20 | 8,316 | 66.3 req/s | 4.2 ms | 27.9 ms | — | 0% |
| Stress | 100 | 19,523 | 251.0 req/s | 18.9 ms | 241.0 ms | 578.9 ms | 0% |

The 100-VU run peaked at 3.46 s for a single request while the database was absorbing the execution backlog, but
the threshold gate (`p95 < 500 ms`, `p99 < 1500 ms`, error rate `< 2%`) passed.

## Execution workloads

| Stage | Submitted | Submit rate | Submit P95 | Peak pending executions | Peak pending tasks | Completed | Throughput | E2E P50 | E2E P95 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Baseline | 100 | 138.8/s | 214 ms | 80 | 79 | 100/100 | 23.6/s | 2.03 s | 3.36 s |
| Moderate | 500 | 146.6/s | 565 ms | 474 | 410 | 500/500 | 24.8/s | 9.84 s | 15.23 s |
| Target | 1,000 | 257.0/s | 418 ms | 986 | 952 | 1,000/1,000 | 25.6/s | 18.95 s | 34.29 s |
| Queue pressure | 5,000 | 200.4/s | 1,260 ms | 4,981 | 4,939 | 4,739 at 205 s | 23.1/s | 110.91 s | 176.13 s |
| Burst | 10,000 | 164.8/s | 2,500 ms | 9,996 | 9,978 | 7,437 at 361 s | 20.6/s | 213.84 s | 310.83 s |
| Python runtime | 100 | 96.4/s | 163 ms | 100 | 92 | 100/100 | 0.7/s | 72.29 s | 129.67 s |

Queue-pressure and burst runs use `-allow-open`; the reported open executions were still in the queue at the
timeout, by design. All accepted submissions succeeded in the baseline, moderate, target and runtime stages.

Retry frequency was 0 for baseline/moderate/target/runtime, 3/4,797 attempts in queue pressure (0.06%) and
5/7,462 in burst (0.07%), all from redelivery under saturation. No failed execution or duplicate success occurred.

## Bottleneck analysis

- The PostgreSQL candidate query is fast (4.49 ms), the execution list query is 1.87 ms and the outbox claim is
  0.16 ms. The database is not the throughput ceiling.
- The echo worker has 64 slots but observed at most 3 active tasks during a 500-execution run. The dispatcher,
  which assigns tasks one transaction at a time, bounds aggregate throughput at about 25 tasks/s per
  orchestrator on this host.
- The outbox publisher can publish 50 messages every 250 ms (200/s) and never saturated.
- NATS carried the dispatch rate without broker pressure; memory stayed at ~138 MB.
- The Python runtime is CPU/startup bound: each task starts a fresh process, and 8 concurrent processes pushed
  attempt P95 to 12.8 s. At 100 executions the end-to-end P50 was 72 s. This is the largest remaining latency
  source for real workloads.

## Optimisations applied

1. `ORCHESTRATOR_DISPATCH_BATCH` and `ORCHESTRATOR_DISPATCH_INTERVAL_MS` are now configurable, and the hard
   batch clamp was raised from 100 to 1,000. Defaults remain 20 every 500 ms.
2. Execution quota limits are parameterised in Compose so load tests can raise active/queued limits without
   editing code.
3. A deterministic Go-only `worker-echo` Compose profile separates orchestration benchmarks from runtime cost.
4. Load-test cleanup reconciles worker slot counters after deleting in-flight fixtures.

No Kafka, extra database or service split was introduced: the measurements do not justify them.

## Next optimisations, in order

1. Run 3+ orchestrator replicas. `DispatchOnce` is concurrency-safe; three replicas should raise dispatch
   throughput roughly linearly.
2. Batch dispatch transactions or parallelise candidate assignment while preserving priority admission.
3. Replace per-task Python process startup with a warm runtime pool, or keep a small pool of pre-imported
   interpreters per worker.
4. Re-run this report against managed PostgreSQL/NATS and a real provider with a small, budgeted workload.

## Reproduce

```bash
docker compose up -d --build postgres nats redis minio minio-init api orchestrator worker
make load-stack
make load-baseline load-moderate load-target
make load-queue-pressure load-burst
make load-runtime
make load-api
./scripts/security-audit.sh
```

Raw results are in `loadtest/results/`.
