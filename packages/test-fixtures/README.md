# Test fixtures

Shared fixtures consumed by Go, Python and JSON Schema tests.

| Path | Purpose |
|---|---|
| `workflows/research.json` | Valid 4-node document used for schema, semantics and digest tests. |
| `workflows/invalid-*.json` | One specific violation each: cycle, unknown edge, non-upstream input, unsafe retry, version 0. |
| `tasks/dispatch.json` | Task dispatch contract. |
| `tasks/result-success.json`, `result-failure.json` | Worker result contracts. |
| `events/*.json` | Envelope + payload for each of the five Phase 1 message types. |
| `workflow.digests.json` | Golden RFC 8785 digests of the valid workflow graph and full document. |

`workflow.digests.json` is the cross-language oracle: Go and Python must independently produce these values after
canonicalizing `workflows/research.json`. If the fixture changes intentionally, recompute the digests with one
implementation, update the file, and confirm both test suites pass. Never update only one language.

Fixtures contain no secrets, no tenant data and no external URLs. Unit-local fixtures stay beside their tests.
