# Shared contracts (Go)

`packages/contracts` is the Go implementation of the workflow, task and event contracts. The JSON Schemas in
[`../schemas`](../schemas) are the structural source of truth; this package adds the rules JSON Schema cannot
express and the canonical digest used for immutable versions.

| File | Contents |
|---|---|
| `workflow.go` | `WorkflowDocument`, `WorkflowGraph`, node/edge/retry models, parse and digest entry points. |
| `workflow_validate.go` | DAG, edge references, upstream `input_from`, retry safety and limits. |
| `task.go` | Task dispatch and result contracts with fenced result validation. |
| `event.go` | Envelope and the five Phase 1 payload types, plus `ParseEvent`. |
| `canonical.go` | RFC 8785 canonicalization and `sha256:` digests. |
| `validation.go` | Shared ID/digest patterns and error aggregation. |

The Python mirror is [`runtimes/python/src/contracts`](../../runtimes/python/src/contracts). Both are tested against
the same fixtures in [`../test-fixtures`](../test-fixtures) and produce identical canonical digests.

```bash
go test ./packages/contracts
go test ./packages/contracts -run TestWorkflowCanonicalDigestsMatchPython -v
```

Generated TypeScript types and API DTOs stay with the dashboard until a generator is justified; do not hand-copy
these models into another language.
