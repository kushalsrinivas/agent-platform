# ADR 0009: Agent versioning

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [architecture.md](../architecture.md), [data-ownership.md](../data-ownership.md), [ADR 0007](0007-workflow-graph-representation.md)

## Context

An agent is defined by a model, instructions, tool bindings, runtime limits and a Python runtime image. If any of
these can change while an execution is running, the execution becomes irreproducible and debugging becomes
guesswork. "Latest" references are convenient but make an execution depend on mutable state.

We need edits to be cheap, published versions to be immutable, and every invocation to be traceable to an exact
definition and runtime.

## Decision

- Each agent has a monotonic integer version. `agent_versions` rows are **insert-only**. A draft is a candidate
  version, not a mutable scratchpad: editing it means creating a new version. The only permitted updates are status
  transitions (`draft → published → deprecated`).
- A version contains:
  - model provider, model name, parameters and a **credential reference name** (never a secret value);
  - instructions/prompt content;
  - exact tool bindings (`tool_id + version + config`);
  - runtime `abi_version` and **image digest** (`sha256:...`, not a mutable tag);
  - limits (turns, timeout), checkpoint policy and optional output schema.
- Every version has a **content digest** (SHA-256 over canonical JSON of the spec + runtime ABI). The digest is
  returned by the API, stored on tasks/attempts and included in logs.
- Workflow versions pin exact agent versions. `latest` is resolved only at **workflow publish time**, never at
  execution time. An execution's tasks copy the exact `agent_version_id`. The scheduler matches the version's
  runtime ABI and image digest against the worker's advertised capabilities; a worker without the image never
  receives the task.
- Publishing requires referenced tools to exist and be published, the runtime ABI and image digest to be supported
  by at least one worker pool, and the credential reference to exist.
- Editing an agent creates a new version; it does not mutate any existing version. Deprecating prevents new
  workflow publication from referencing it but does not affect existing workflow versions or executions: a pinned
  graph may still use a deprecated agent version. Only the API's publish-time validation requires `published`.
- Model/provider aliases are allowed only if they are stable model IDs. Floating provider aliases (e.g. "latest")
  are rejected unless the author pins a concrete model name; provider-side model changes are outside our control
  and must be visible in the version.
- Secrets are project-scoped credentials referenced by name. Rotating a credential does not change the version
  digest; it changes the value resolved at attempt start, audited separately.

## Consequences

**Positive**

- An execution is structurally reproducible: same graph, same agent specs, same runtime image digest, same input.
- Debugging can compare digests and know exactly what ran.
- Rollback is selecting an older version, not editing anything.
- Different teams can publish new versions without breaking in-flight work.

**Negative**

- Any change, even a prompt typo fix, creates a new version; storage grows (small rows, acceptable).
- Workflow authors must republish to pick up a new agent version; no auto-upgrade.
- Runtime image digests add a build/publish step and require an image registry.
- Deprecation and cleanup need policy; Phase 1 retains published versions indefinitely.

## Alternatives considered

- **Mutable agent definitions with an audit log.** Easy to edit, but cannot guarantee what an execution used;
  logs become the only truth. Rejected.
- **Semver ranges (`^1.2`).** Convenient but non-deterministic at run time and hard to reason about; exact integer
  versions are simpler.
- **Git refs as versions.** External dependency, mutable refs, no atomic link to tool/runtime config.
- **Version by content hash only.** Avoids duplicate rows but complicates UI ordering and references; we keep a
  monotonic version plus a digest.
- **Embed secrets in the version.** Leaks into API responses/history and makes rotation require a new version.
  Rejected.

## Verification

- API/DB tests reject updates to a published version's spec/digest.
- Workflow publish tests reject unpublished/missing agent versions and mutable image tags.
- Execution tests prove tasks carry the pinned version and do not re-resolve when a newer version is published
  mid-run.
- Digest tests are canonical across key ordering and whitespace.
- Credential rotation test changes the resolved value without changing the version digest.
