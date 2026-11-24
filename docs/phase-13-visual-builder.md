# Phase 13 — Visual Workflow Builder

Status: complete. A workflow can be built, validated, versioned, deployed and executed from the dashboard canvas
without writing workflow JSON. The builder serializes only Phase 03 graph fields and the server remains the
authority for validation, agent references, digests and publication.

## 1. Routes

| Route | Purpose |
|---|---|
| `/workflows/new` | Blank canvas with a start → end baseline; saving creates the workflow and its first draft. |
| `/workflows/{id}/edit` | Loads the latest version (draft or published), saves immutable draft versions and deploys. |
| `/workflows/{id}` | Existing detail page links to the builder and shows the deployed version. |

The application shell widens the main column for builder routes so the canvas can use the viewport.

## 2. Canvas

React Flow supplies the infinite canvas, pan/zoom, node dragging, edge connections, selection, minimap,
controls, keyboard focus and fit-to-view. The builder adds:

- **Auto-layout:** layered left-to-right placement; runs on load and from the Layout button.
- **Deletion:** Delete/Backspace deletes the selected nodes and their incident edges in one history entry; the
  inspector also deletes.
- **Undo/redo:** up to 100 snapshots, coalescing rapid edits to the same node, drags as one step, and structural
  actions as one step. Shortcuts: `⌘/Ctrl+Z`, `⇧⌘/Ctrl+Z`, `Ctrl+Y`.
- **Copy/paste:** `⌘/Ctrl+C` copies selected nodes and internal edges; `⌘/Ctrl+V` pastes with fresh, unique node
  ids and remapped edges.
- **Save/validate shortcuts:** `⌘/Ctrl+S` saves a draft; `⌘/Ctrl+Enter` validates.
- **Fit and layout:** the Fit button re-frames the graph after manual moves or layout.

Single-node workflows are valid; start/end are optional in the contract but the default canvas and library make
them explicit. Adding the first non-start/end node into a bare start → end graph automatically inserts it into
that edge.

## 3. Node library and inspectors

The library is grouped as Core (start, end, agent, LLM, tool), Logic (condition, parallel, join, loop), Execution
(delay, approval, subworkflow) and Data (transform). Items can be clicked or dragged onto the canvas.

The inspector is type-specific:

- **Agent/LLM:** agent and immutable agent version, provider/model summary, timeout, worker pool, retries,
  retry-on classes, side-effect class, checkpoint mode, idempotency key, params JSON, upstream `input_from`,
  output schema.
- **Tool:** tool version, params JSON, timeout, pool, retries, side effects, checkpoint, output schema.
- **Condition:** CEL expression and branch edges; the first two branch edges default to
  `outputs.<node>.result == true` and `== false`.
- **Transform:** dotted output paths mapped to CEL expressions.
- **Join:** all/successful-N mode, success threshold and partial-failure behaviour.
- **Delay:** seconds, RFC3339 timestamp or external event; exactly one.
- **Approval:** prompt, any/all policy, approvers, expiry and reject behaviour.
- **Subworkflow/loop:** child workflow and version, child input mapping; loops add a bounded iteration count
  and CEL exit condition.
- **Common:** node id rename with edge, `input_from`, `output_from` and `outputs.<id>` expression rewriting;
  outgoing edge condition, priority and data mapping; retry/resources/node JSON escape hatches.

Switch, merge, filter and validate are not separate Phase 03 node types. The library notes their mappings:
switch → condition with branch expressions, merge → join, filter/validate → transform (or an approval/agent step
when a human or model decision is required).

## 4. Validation

Local validation runs as the graph changes and reports duplicate or invalid ids, missing worker references,
invalid configs, unreachable nodes, missing entry/termination, self/duplicate edges, cycles, upstream input
mistakes, retry limits and unbounded-loop risks. Clicking an issue selects and centres the affected node.

The Validate action calls `POST .../workflows:validate`. The handler now also compiles the workflow input schema
and every node input/output schema, so malformed schemas are caught before persistence. The same server
validator and schema compiler run on create, new version and publish; the frontend never authorises deployment.

Server problems are parsed back into the list. `nodes[i]`, `edges[i]` and `node "id"` references map to the
corresponding canvas node.

## 5. Serialization

`serializeGraph` emits exactly:

```json
{
  "nodes": [{"id": "...", "type": "...", "agent": {}, "config": {}, "retry": {}, "...": "..."}],
  "edges": [{"from": "...", "to": "...", "when": "...", "data_mapping": {}, "priority": 0}],
  "policy": {},
  "input_schema": {}
}
```

Node positions, selection, zoom and execution state are client-only and never sent. Node ids are preserved
across edits. Saving always creates a new immutable version; a digest collision with the previous version is
reported as a conflict.

## 6. Versioning, deployment and rollback

- **Save draft:** creates a draft version and updates `latest_version`.
- **Validate:** server-side structural/schema validation without storing.
- **Deploy:** publishes the current draft (saving first if the canvas is dirty) or re-activates the current
  published version when there are no changes.
- **Compare:** select any two versions to see added/removed/changed nodes and edges plus policy/input-schema
  changes.
- **Rollback:** the Versions panel activates any older published version. `PATCH publish` on an already-published
  version now updates `control.workflows.deployed_version` instead of being a no-op.

Migration `control/0006_workflow_deployment` adds `deployed_version`. Publishing a draft sets the pointer and
deploying an older published version moves it back. Executions still record and use their explicit
`workflow_version`, so a running execution remains pinned to its original graph.

## 7. Interactive execution

Run is available when the canvas is clean and the workflow exists. The dialog lists published versions, defaults
to the deployed version and seeds an input skeleton from the workflow input schema. Starting a run keeps the
builder open:

- The canvas overlays task state and duration on each node and highlights failures/validation errors.
- The Execution panel follows the persisted SSE stream, shows the selected node's attempts, worker, metrics,
  errors, checkpoint and output, and renders the execution input and latest events.
- Clicking a node while a run is present opens the Execution panel for that node.
- Cancel is available while the execution is active; Explorer opens the full Phase 12 view.

Retries appear as attempt rows with error code/message, worker and metrics. Worker process logs and stack traces
remain unavailable because the platform does not persist them.

## 8. Verification

- **Exit criterion:** a headless-browser run added an agent from the library (auto-inserted between start/end),
  selected a published agent version, validated, saved a draft, deployed it and executed it from the builder. The
  canvas showed succeeded nodes and the Execution panel showed the attempt, tokens, duration and output. No
  workflow JSON was typed.
- **Versioning:** the same run edited the agent timeout, saved v2, deployed it, activated v1 (rollback), and
  compared v1 → v2 (one changed node).
- **Canvas behaviour:** copy/paste, undo, redo, delete, condition insertion and clicking a validation error to
  select the affected node were exercised.
- **Backend:** `TestDeveloperAPIKeySDKExitCriterion` now asserts `deployed_version` after publish, v2 publish and
  v1 rollback. The full integration suite, Go unit tests, dashboard lint/build and OpenAPI document pass.

## 9. Known limits

- Node positions are not part of the Phase 03 graph and are not persisted; reloading auto-layouts.
- Every save creates a new immutable version; drafts are not edited in place.
- Switch/filter/validate semantics are composed from condition/join/transform rather than dedicated engine nodes.
- Per-node execution priority and tool credential references are not node-level fields in the current contract;
  execution priority is set on submission and credentials belong to the agent/tool version.
- CEL syntax is compiled by the server; the local panel checks required/empty expressions only.
- Schema diffing compares node/edge structure, not field-level JSON schema semantics.
