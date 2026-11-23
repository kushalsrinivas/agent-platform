# ADR 0006: Idempotency and external side-effect handling

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [state-machines/task.md](../state-machines/task.md), [failure-and-recovery.md](../failure-and-recovery.md)

## Context

At-least-once execution (ADR 0004) means an attempt can run more than once after a crash, partition or lease
expiry. Internal state is protected by fences, but a tool call that sends an email, charges a card, creates a pull
request or deploys a service is not. Checkpoints do not make a sequence of tool calls transactional: resuming from
a checkpoint can replay any calls made after the checkpoint. Retrying blindly is therefore a correctness and
business risk.

The platform cannot know whether an arbitrary external API is idempotent. The workflow author can. We need a
contract that forces that knowledge to be declared at publish time and enforces safe retry policy at runtime.

## Decision

### Idempotency layers

1. **API submissions:** `Idempotency-Key` unique per org; duplicate returns the original execution.
2. **Dispatch/commands:** keyed by `attempt_id`; duplicates are dropped by the worker/fence.
3. **Results:** fenced and committed once; duplicates return `ALREADY_FINISHED`/`STALE_LEASE`.
4. **Events:** deduped by per-execution `seq`; never used to derive state.
5. **External effects:** declared per node and enforced by retry policy (below).

### Node side-effect declaration

Every agent or tool node declares `side_effect_class`:

| Class | Meaning | Retry rule |
|---|---|---|
| `read_only` | No external mutation (or mutations are irrelevant). | Retries allowed by normal policy. |
| `idempotent` | The effect can be safely repeated because the external system de-duplicates on a stable key. | Allowed only if `idempotency_key` is present and the effect is actually keyed by it. |
| `non_idempotent` | Repeating may cause a new unwanted effect. | `max_attempts = 1` and no retry on `worker_lost`, `cancelled` or timeout after execution started, unless the workflow author explicitly supplies a key and declares the tool idempotent. |

- The idempotency key is a CEL expression over execution/node inputs, e.g.
  `execution.id + ":" + node.id + ":" + input.customer_id`. It **must not** include `attempt_id`: that would make
  every retry unique and defeat the purpose.
- Publishing validates class/retry combinations. An `idempotent` node without a key is rejected. A
  `non_idempotent` node with `max_attempts > 1` or `retry_on` containing `worker_lost`/`cancel` is rejected.
- An agent node is classified by the **strictest** class among its tool bindings and any tool it may invoke. If an
  agent can call a non-idempotent tool, the agent node is non-idempotent. The runtime enforces which tools it can
  call; the author cannot hide a side effect behind an agent loop.
- Tool calls inside an agent loop are not platform tasks and are not independently retried. The task attempt is the
  retry unit. This is why the declaration is at node level.

### Effect uncertainty

When a started attempt is abandoned (worker loss, cancellation, unreachable worker), the attempt is marked
`effect_uncertain = true` if the node is not `read_only`. The task then:

- retries only if the declared policy permits it (idempotent key or explicit opt-in); otherwise fails with
  `EFFECT_UNCERTAIN`/`WORKER_LOST`;
- exposes the flag through the API, events and dashboard. Operators must treat it as "a side effect may have
  happened".

The platform does **not** provide compensation, rollback or a distributed transaction. Compensation is a workflow
the author models (e.g. a cleanup node) and it is best effort.

### Checkpoints

A checkpoint is a resume optimisation, not a transaction boundary. Resuming from checkpoint N may replay effects
performed after N. Workflow authors must place idempotency keys on any node where replay is undesirable, or disable
retry. This is stated in the checkpoint contract (ADR 0008).

## Consequences

**Positive**

- The dangerous default is safe: a node without an idempotency declaration is not retried after an uncertain
  attempt.
- Publishing catches unsafe combinations before they run.
- Operators can see uncertainty instead of silently duplicating effects.
- The platform makes a claim it can keep: exactly-once state, at-most-once effect where declared.

**Negative**

- Workflow authors must understand their tools' idempotency; the platform cannot infer it.
- Non-idempotent nodes reduce availability: a worker crash fails the task rather than retrying automatically.
- Strict tool classification may force broader agent-node classification and reduce retry options.
- Correctness depends on the external system honouring the idempotency key; the declaration is a contract, not a
  proof.

## Alternatives considered

- **Retry everything and hope tools are idempotent.** Unacceptable for payments, notifications and deployments.
- **Exactly-once effect via a distributed transaction/2PC.** Most external APIs do not support it; would not cover
  HTTP side effects.
- **Only platform-level idempotency, no per-node declaration.** Cannot distinguish read-only from charging a card.
- **Make every node non-retryable by default.** Safe but makes transient worker failures unrecoverable and harms
  reliability; read-only work should retry.
- **Checkpoint as a transactional boundary.** False: checkpoints do not fence external APIs and may be stale.

## Verification

- Publish validation rejects `idempotent` without a key and `non_idempotent` with retries.
- Integration tests simulate a crash after a side-effecting tool call and assert no automatic retry for a
  non-idempotent node, and `effect_uncertain` is visible.
- Idempotency-key derivation tests include retries and prove the key is stable across attempts.
- Agent classification tests with mixed tool bindings produce the strictest class.
