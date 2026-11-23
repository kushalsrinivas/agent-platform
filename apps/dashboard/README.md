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
| App shell, sidebar, header | `src/components/layout/` |

State: TanStack Query owns server state (currently fixtures), Zustand owns the execution drawer selection.
Zod validates API-shaped fixtures and the new-execution form input.

## Phase status

The shell renders fixture data from `src/lib/demo-data.ts`, labelled "demo data" in the UI. The REST/SSE wiring
lands in Phase 05; no fake fetch layer exists to unwind.

```bash
npm ci
npm run dev        # http://localhost:3000
npm run lint       # biome + tsc
npm run build
```
