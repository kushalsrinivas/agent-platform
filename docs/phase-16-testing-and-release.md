# Phase 16 — Testing, benchmarking, chaos and release

Status: release-candidate evidence complete. The `v0.1.0-rc.1` tag is prepared but intentionally not created
until the working tree is reviewed and committed.

## Deliverables

| Deliverable | Location |
|---|---|
| Unit test suite | Go `*_test.go`, Python `tests/` |
| Integration suite | `tests/integration/` (44 tests) |
| E2E workflows A–H | mapped below, real HTTP/Postgres/NATS/runtime |
| Chaos suite | `tests/integration/chaos_test.go` + recovery/messaging suites |
| Load testing | `cmd/loadgen`, `loadtest/k6/api.js`, `loadtest/results/` |
| Performance report | [performance-report.md](performance-report.md) |
| Security audit report | [security-audit.md](security-audit.md) |
| Deployment automation | `.github/workflows/release.yml`, `Makefile`, `scripts/` |
| Operational runbooks | [runbooks.md](runbooks.md) |
| Release process | [release-process.md](release-process.md) |
| Release candidate | `CHANGELOG.md` 0.1.0-rc.1 (tag pending) |

## 16.1 Unit tests

`go test -race ./...` covers 50 Go tests; Python runs 69 pytest cases. Pure policy, validation, retry and
serialisation logic has isolated tests. Transactional state machines and scheduler paths are additionally
verified against the real PostgreSQL schema, where their guards live:

| Area | Tests |
|---|---|
| Execution/task state machines | `lifecycle_test.go` guards plus `schema_test.go`/`api_flow_test.go` real-schema transitions |
| Scheduler and leases | `lifecycle_test.go`, worker-manager integration tests |
| Retry policies | `services/orchestrator/internal/lifecycle/retry_test.go` |
| Workflow validation | `services/api/internal/workflows/schema_test.go` |
| Dependency resolution | `services/orchestrator/internal/graph/executor_test.go` |
| Idempotency | `services/api/internal/executions/handlers_test.go`, schema constraints |
| Tenant authorization | `services/api/internal/auth/auth_test.go`, `middleware_test.go` |
| Event publisher/outbox | `internal/outbox/outbox_test.go`, `internal/messaging/messaging_test.go`, outbox integration contract |
| Agent lifecycle, cancellation, usage, output | `runtimes/python/tests/test_agent_runtime.py`, `test_grpc_service.py` |
| LangGraph adapter | `test_langgraph_runtime.py` |
| Provider adapters | `test_model_providers.py` |
| Tools and MCP | `test_tool_runtime.py`, `test_tool_policy.py`, `test_mcp_manager.py` |
| Checkpoint serialization/restoration | `test_checkpointing.py` |

## 16.2 Integration boundaries

All tests run against real services after `make integration-test`; only model providers are deterministic.

| Boundary | Evidence |
|---|---|
| API → PostgreSQL | `api_flow_test.go`, `schema_test.go`, `security_flow_test.go` |
| Orchestrator → NATS | `infrastructure_test.go`, `messaging_flow_test.go` |
| Orchestrator → Python worker | `runtime_flow_test.go`, `worker_concurrency_test.go` |
| Worker → provider | `runtime_flow_test.go`, `chaos_test.go` provider outage |
| Worker → MCP | `mcp_flow_test.go` (real stdio fixture server) |
| Orchestrator → artifact store | checkpoint upload/restore paths via the filesystem artifact store; an S3/MinIO adapter remains a known Phase 15 limit |
| Dashboard → API | dashboard lint/build, live page and proxy (`/api/platform/orgs` returned 200) |
| SDK → API | `python_sdk_test.go`, `developer_api_test.go` |

## 16.3 End-to-end workflows

| Required workflow | Test |
|---|---|
| A. Start → Agent → End | `TestRuntimeLangGraphAgentExecution` |
| B. Start → Research → Analysis → Writer → End | `TestGraphEngineTenNodeWorkflow` (sequential agent stages) |
| C. Start → three parallel agents → Join → End | `TestGraphEngineTenNodeWorkflow` fan-out/join, `TestWorkerExecutesCapacityConcurrently` |
| D. Conditional paths | `TestGraphEngineTenNodeWorkflow` (condition branch), `TestGraphSubworkflowLoopApprovalDelayReplay` (loop) |
| E. Human approval → resume → end | `TestApprovalAssignmentPolicyAuditAndExpiry`, `TestApprovalExpiryReleasesExecution` |
| F. Supervisor → three child agents → aggregate | `TestSupervisorDelegatesConcurrentlyAndRecovers` |
| G. Agent → MCP tool → Agent → End | `TestMCPDiscoveryInvocationAndPolicy` |
| H. Delay → resume → end | `TestGraphSubworkflowLoopApprovalDelayReplay` |

## 16.4 Chaos matrix

| Failure | Expected | Test | Result |
|---|---|---|---|
| Kill worker during execution | lease expiry and recovery | `TestRecoveryWorkerDiesBeforeReceiving`, `...AfterReceiving`, `...DuringModelGeneration` | pass |
| Kill worker after result commit | duplicate handled safely | `TestMessagingRedeliveryAndDuplicateSuppression` | pass |
| Kill orchestrator | durable state restored | `TestRecoveryOrchestratorRestart` | pass |
| Restart NATS | persistent messages recover | `TestRecoveryNATSReconnect` | pass |
| Interrupt PostgreSQL | no silent corruption | `TestRecoveryDatabaseUnavailable` | pass |
| Disconnect provider | controlled retry/backoff | `TestChaosProviderUnavailableRetriesAndFailsSafely` | 2 attempts, durable failure in 5.6 s |
| Expire lease | stale worker fenced | `TestWorkerManagerStaleWorkerCannotCommit` | pass |
| Restart Kubernetes pod | work resumes safely | `TestChaosWorkerPodRestart` (Compose worker equivalent) | 9/9 succeeded in 80 s, no duplicate success |
| Corrupt checkpoint | fail safely / never resume unverified | `test_checkpointing.py`, `test_grpc_service.py`, checkpoint validation tests | pass |
| Duplicate execution request | one logical execution | `TestChaosConcurrentDuplicateExecutionRequest` | 10 concurrent, 1 execution |
| Cancel during parallel execution | children cancelled | `TestChaosCancelDuringParallelExecution` | 3/3 cancelled in 1.2 s |
| Deploy new runtime version | existing execution compatible | ABI pinning, checkpoint schema/hash tests | protocol verified; live rolling upgrade not run |

The worker-pod test uses a 3-attempt retry budget. With `max_attempts=1`, a killed worker is terminal by design;
read-only agents intended to survive pod restarts should set at least 3 attempts.

## 16.5 Load stages

Executed: baseline 100, moderate 500, target 1,000 concurrent, queue pressure 5,000, burst 10,000, Python
runtime 100, API 20 and 100 VUs. Not executed: the multi-hour sustained run (the k6 and loadgen commands support
it) and the optional real-provider test (no provider key was used). See
[performance-report.md](performance-report.md) for tables and bottlenecks.

## 16.6 Benchmarks

API: 251 req/s, P50 18.9 ms, P95 241 ms, P99 579 ms, 0% errors at 100 VUs.
Orchestration: 25.6 executions/s at 1,000 concurrent; end-to-end P50 18.95 s, P95 34.29 s.
Python runtime: 0.7 executions/s; process startup dominates. Queue pressure reached 4,981 concurrent executions
with no failures. Full numbers and caveats are in the performance report.

## 16.7 Optimisations and decisions

- Added configurable `ORCHESTRATOR_DISPATCH_BATCH` / `ORCHESTRATOR_DISPATCH_INTERVAL_MS`; raised the clamp from
  100 to 1,000. Defaults are unchanged (20 every 500 ms).
- Parameterised execution quotas in Compose for load testing.
- Added a deterministic `worker-echo` loadtest profile to separate orchestration from Python runtime cost.
- Repaired worker slot counters in destructive load-test cleanup.
- No Kafka, second database or service split: the measured database/NATS latency does not support that cost.
- Remaining bottleneck is the single dispatcher's per-task transaction; scale orchestrator replicas and/or batch
  dispatch next. Python per-task process startup is the runtime bottleneck; a warm pool is the next runtime
  optimisation.

## 16.8 Release engineering

- Semantic versioning and `CHANGELOG.md`; release workflow on `v*` tags.
- Full CI gate including unit, integration, manifests, image build/scan and security audit.
- Release job builds six images, scans, pushes to GHCR, packages the Helm chart to OCI and creates release notes.
- `make migrate-check` applies every migration to a scratch database; `make backup-verify` restores a dump and
  compares core table counts.
- Helm rollback and database restoration procedures are documented in `release-process.md` and `runbooks.md`.
- The `v0.1.0-rc.1` tag is intentionally not created in this working tree.

## 16.9 Operational documentation

Ten runbooks: orchestrator outage, NATS outage, database outage, worker fleet failure, queue backlog, provider
outage, corrupt execution, failed deployment, secret rotation and database restoration, plus escalation and
first-response guidance.

## Verification evidence (2026-11-24)

- `go test -race -count=1 ./...`, `go vet ./...` and `golangci-lint run` clean.
- Python Ruff/format/mypy clean; 69 pytest cases pass.
- Dashboard lint and production build pass; the live dashboard proxy reached the API successfully.
- `helm lint`/`helm template`, `kubectl kustomize`, and `helm package` pass; chart version `0.1.0-rc.1`.
- `make migrate-check` applies control=7 / execution=8 on a scratch database.
- `make backup-verify` restores a 1.3 MB dump and matches core table counts.
- `./scripts/security-audit.sh` passes all scans.
- Full integration suite, including the chaos tests, passes in 251.8 s.

## Exit criteria

| Gate | Status |
|---|---|
| Unit suite | pass |
| Integration suite (real boundaries) | pass |
| E2E A–H | pass |
| Chaos matrix | pass, with the runtime-upgrade caveat |
| Load baseline through target | pass |
| 5,000 queue pressure / 10,000 burst | pass (submission and queueing; drain continues beyond the timeout) |
| Performance report | complete |
| Security audit | pass, with accepted dev-only base-manifest risk |
| Deployment automation | complete; tag pending |
| Operational documentation | complete |
| Production-ready label | **not claimed** |

Not claimed because KEDA/Helm have not been applied to a live cluster, no real-provider run has been made, and
the artifact backend is filesystem-only (no MinIO/S3 adapter yet).
The release candidate is suitable for a staging cluster with those two checks as the next gate.
