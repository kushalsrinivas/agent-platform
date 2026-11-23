# Chaos experiments (deferred)

Planned experiments, each tied to a documented recovery path:

| Experiment | Expected behaviour |
|---|---|
| Kill a worker mid-attempt | Lease expires after 30s; attempt recorded `lease_expired`; task retried or failed per policy. |
| Restart NATS with an in-flight dispatch | Work pauses; outbox republishes; no duplicate logical transition. |
| Kill the orchestrator during result commit | Transaction rolls back; worker retry is fenced and idempotent. |
| Duplicate every dispatch message | Worker accepts at most one attempt per lease; duplicates are discarded. |
| Pause PostgreSQL | Services fail readiness, do not accept new executions; recovery needs no manual state repair. |

The expected behaviour is normative in
[failure-and-recovery.md](../../docs/failure-and-recovery.md); this suite will prove it under fault injection.
