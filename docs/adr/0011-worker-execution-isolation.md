# ADR 0011: Worker execution isolation

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [service-responsibilities.md](../service-responsibilities.md), [ADR 0010](0010-tenant-isolation.md)

## Context

The Python runtime executes agent loops and tools, potentially including tenant-authored prompts and, eventually,
tenant-supplied tool code or dependencies. It must not be able to read other tenants' data, persist credentials,
escape its resource budget, or hold the worker hostage. At the same time, Phase 1 runs on developer laptops and a
single Linux host; nested container runtimes add significant complexity.

We need an isolation level that is honest about its limits and a clear trigger for strengthening it.

## Decision

### Phase 1: per-attempt process sandbox

Each task attempt runs in a fresh Python process group created by the worker:

- dedicated temporary working directory, removed after the attempt;
- environment scrubbed to an allowlist; only task-scoped credentials are injected, in memory;
- unprivileged OS user where available; `no_new_privs`, `PR_SET_PDEATHSIG` equivalent, resource limits
  (`RLIMIT_AS`, `RLIMIT_CPU`, `RLIMIT_NOFILE`, `RLIMIT_NPROC`);
- Linux: cgroup v2 limits for memory, CPU and pids; seccomp allowlist in production; no capability escalation;
- no database, NATS or object-storage credentials; only a per-process UDS, a presigned URL when needed, and
  short-lived task credentials obtained by the worker;
- per-attempt deadline enforced by the worker (`SIGTERM`, 5s grace, `SIGKILL` of the whole process group);
- network egress restricted to an allowlist/proxy in production; development may allow broader egress with a
  warning;
- logs to stdout captured by the worker with size limits; no secrets in logs;
- orphan cleanup by process group/cgroup at worker startup and attempt end.

### What this is not

- It is **not** a security boundary against a determined attacker on a shared kernel. A kernel exploit or a
  container-escape class bug is out of scope for Phase 1.
- It is **not** safe for arbitrary tenant-supplied code execution. Phase 1 assumes workflows are authored by
  trusted tenant users and tools are platform-provided or vetted.
- macOS development lacks cgroups/seccomp; local runs are for development, not hostile multi-tenant use.

### Upgrade trigger

Move each attempt into a container or microVM (Firecracker/Kata/gVisor, or a managed sandbox service) when **any**
of the following is true:

1. tenants can supply or install their own tool code/dependencies;
2. the platform is offered to untrusted or regulated tenants on shared workers;
3. a kernel-level isolation finding requires it;
4. per-attempt memory/CPU enforcement cannot be met with cgroups on the deployment target.

The runtime contract does not change: the sandbox still speaks the same UDS gRPC protocol and returns checkpoints.

### Credentials and data

- Credentials are fetched by the worker at attempt start, scoped to the task, TTL-bounded and memory-only.
- The sandbox can read only the task context and its own checkpoint; object access is presigned and scoped.
- Sandbox files are deleted after the attempt. A crash triggers worker startup cleanup before accepting new work.

## Consequences

**Positive**

- Low overhead: no nested container runtime, fast startup, simple local development.
- Resource limits and deadlines prevent the common failure modes (runaway loops, memory exhaustion, hung tasks).
- The runtime contract is stable if isolation is strengthened later.

**Negative**

- Shared kernel and shared worker process mean a serious exploit could cross tenants. This must be disclosed in
  the security model and revisited before untrusted multi-tenancy.
- Egress control and seccomp require production configuration; development does not exercise them.
- A process can still consume CPU briefly before limits apply; monitoring is required.
- Tool-level permissions are enforced in the runtime, not by hardware isolation.

## Alternatives considered

- **One container per attempt now.** Stronger, but requires a container runtime on every worker (including local
  development) and nested-container operational complexity. Deferred with a clear trigger.
- **MicroVM per attempt now.** Strongest isolation, highest startup/ops cost; premature for Phase 1.
- **WASM sandbox.** Great for pure computation, but Python/LLM/tool ecosystems and native dependencies make it
  impractical now.
- **Shared long-lived Python process.** Fast but leaks state between attempts, makes cancellation unreliable and
  weakens tenant isolation. Rejected.
- **No sandbox, just threads.** Unacceptable for memory limits and cleanup.

## Verification

- Tests assert attempt processes cannot see env secrets, other attempts' temp dirs, DB/broker ports, or persist
  files after completion.
- Deadline tests prove SIGTERM→SIGKILL and no orphan process remains.
- Resource-limit tests allocate beyond the memory cap and observe a controlled failure, not worker death.
- Worker restart cleanup test leaves an orphan and proves it is killed before new dispatches are accepted.
- Upgrade trigger is reviewed at each security/tenancy milestone; the security model must not claim container
  isolation until it exists.
