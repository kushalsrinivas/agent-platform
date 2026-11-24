# Dashboard

Next.js 16 dashboard for the Agent Platform.

## Design system

All tokens live in `src/app/globals.css` as CSS custom properties and are exposed to Tailwind through
`@theme inline`. Light and dark themes set the same variable names; `next-themes` toggles the `dark` class.

| Category | Tokens |
|---|---|
| Surfaces | `background`, `card`, `popover`, `muted` |
| Text | `foreground`, `card-foreground`, `muted-foreground` |
| Actions | `primary`, `secondary`, `ring` |
| Feedback | `success`, `warning`, `danger`, `info` |
| Borders | `border`, `input` |
| Type | `--font-sans` (Geist Sans), `--font-mono` (Geist Mono) |
| Radius | `--radius-sm`, `--radius-md`, `--radius-lg` |

Spacing uses Tailwind's 4px scale. Status colour is never the only signal: `StatusIndicator` always renders a
text label (or screen-reader text).

## Components

| Component | Location |
|---|---|
| Button, Badge, Card, Table | `src/components/ui/` |
| Modal, Drawer (Radix Dialog) | `src/components/ui/` |
| Toasts (`sonner`) | `src/components/ui/toast.tsx` |
| Copyable code block | `src/components/ui/code-block.tsx` |
| Execution status | `src/components/ui/status-indicator.tsx` |
| Workflow graph + custom nodes | `src/components/graph/workflow-graph.tsx` |
| Visual workflow builder | `src/components/builder/` and `src/lib/builder/` |
| App shell, sidebar, header | `src/components/layout/` |

State: TanStack Query owns server state, Zustand owns the workspace scope, API key and drawer selection. The
same-origin proxy at `src/app/api/platform/[...path]/route.ts` forwards REST and SSE to the platform API;
`DASHBOARD_API_URL`, `DASHBOARD_API_KEY` and `DASHBOARD_TOKEN` configure the server side. Without a server
credential, connect with a project-scoped `apk_…` operator key from the dashboard dialog or `/settings`.

## Phase status

Phase 12 wires the dashboard to the developer API: overview analytics, agent and workflow catalogs and details,
the live execution explorer with SSE reconnect, node/model/failure inspectors, logs, approvals, workers, tools,
MCP, usage and settings. Phase 13 adds the visual builder at `/workflows/new` and `/workflows/{id}/edit`, with
canvas editing, type-specific inspectors, validation, version comparison, deploy/rollback and live execution.
See `docs/phase-12-dashboard.md` and `docs/phase-13-visual-builder.md` at the repository root.

```bash
npm ci
npm run dev        # http://localhost:3000
npm run lint       # biome + tsc
npm run build
```
