import {
  type Execution,
  executionSchema,
  type GraphEdge,
  type GraphNodeData,
  graphEdgeSchema,
  graphNodeSchema,
  type Workflow,
  workflowSchema,
} from "@/lib/schemas";

/**
 * Demo dashboard data. Everything on screen is labelled as demo data; Phase 05 replaces these exports with
 * TanStack Query calls against the API. Keeping the fixtures validated by the same Zod schemas the API will
 * satisfy means the UI cannot drift into a shape the API never promised.
 */
export const demoExecutions: Execution[] = executionSchema.array().parse([
  {
    id: "exec_01JQ8Z3K4M5N6P7Q8R9S0T1V",
    workflowName: "research-brief",
    workflowVersion: 7,
    status: "running",
    startedAt: "2026-11-23T08:41:00Z",
    durationMs: 42_000,
    triggeredBy: "kushal@local",
    input: { topic: "Durable execution patterns", depth: "standard" },
    output: null,
  },
  {
    id: "exec_01JQ8Y1A2B3C4D5E6F7G8H9J",
    workflowName: "support-triage",
    workflowVersion: 3,
    status: "succeeded",
    startedAt: "2026-11-23T08:22:00Z",
    durationMs: 8_412,
    triggeredBy: "scheduler",
    input: { ticket: "TCK-1042", channel: "email" },
    output: { queue: "billing", priority: "high", draft_reply_id: "art_9f2" },
  },
  {
    id: "exec_01JQ8X7M8N9P0Q1R2S3T4V5W",
    workflowName: "code-review-agent",
    workflowVersion: 12,
    status: "failed",
    startedAt: "2026-11-23T07:58:00Z",
    durationMs: 31_905,
    triggeredBy: "ci",
    input: { pull_request: 418, repository: "acme/checkout" },
    output: { error: "tool timeout: github.fetch_files" },
  },
  {
    id: "exec_01JQ8W2C3D4E5F6G7H8J9K0L",
    workflowName: "research-brief",
    workflowVersion: 7,
    status: "succeeded",
    startedAt: "2026-11-22T18:10:00Z",
    durationMs: 96_140,
    triggeredBy: "kushal@local",
    input: { topic: "Postgres outbox patterns", depth: "deep" },
    output: { brief_id: "art_7c1", sources: 14 },
  },
  {
    id: "exec_01JQ8V4N5P6Q7R8S9T0V1W2X",
    workflowName: "invoice-extractor",
    workflowVersion: 5,
    status: "queued",
    startedAt: "2026-11-22T17:55:00Z",
    durationMs: 0,
    triggeredBy: "scheduler",
    input: { object_key: "inbox/2026/11/inv-8821.pdf" },
    output: null,
  },
  {
    id: "exec_01JQ8T6B7C8D9E0F1G2H3J4K",
    workflowName: "support-triage",
    workflowVersion: 3,
    status: "cancelled",
    startedAt: "2026-11-22T16:40:00Z",
    durationMs: 4_020,
    triggeredBy: "operator",
    input: { ticket: "TCK-1039", channel: "chat" },
    output: null,
  },
]);

export const demoWorkflows: Workflow[] = workflowSchema.array().parse([
  {
    id: "wf_01JQ7RESEARCH",
    name: "research-brief",
    version: 7,
    status: "published",
    nodeCount: 5,
    updatedAt: "2026-11-21T12:30:00Z",
  },
  {
    id: "wf_01JQ7TRIAGE",
    name: "support-triage",
    version: 3,
    status: "published",
    nodeCount: 4,
    updatedAt: "2026-11-19T09:10:00Z",
  },
  {
    id: "wf_01JQ7REVIEW",
    name: "code-review-agent",
    version: 12,
    status: "published",
    nodeCount: 6,
    updatedAt: "2026-11-23T06:05:00Z",
  },
  {
    id: "wf_01JQ7INVOICE",
    name: "invoice-extractor",
    version: 5,
    status: "draft",
    nodeCount: 3,
    updatedAt: "2026-11-20T14:45:00Z",
  },
]);

export const demoGraphNodes: GraphNodeData[] = graphNodeSchema.array().parse([
  {
    id: "collect",
    label: "collect-source",
    kind: "tool",
    status: "succeeded",
    durationMs: 1_240,
  },
  {
    id: "plan",
    label: "plan-questions",
    kind: "agent",
    status: "succeeded",
    durationMs: 4_810,
  },
  {
    id: "search",
    label: "search-web",
    kind: "tool",
    status: "running",
    durationMs: 22_500,
  },
  {
    id: "synthesize",
    label: "synthesize-brief",
    kind: "agent",
    status: "queued",
  },
  {
    id: "publish",
    label: "publish-artifact",
    kind: "tool",
    status: "queued",
  },
]);

export const demoGraphEdges: GraphEdge[] = graphEdgeSchema.array().parse([
  { id: "e1", source: "collect", target: "plan" },
  { id: "e2", source: "plan", target: "search" },
  { id: "e3", source: "search", target: "synthesize", label: "results" },
  { id: "e4", source: "synthesize", target: "publish" },
]);

export const demoEvents = [
  {
    id: "evt_8",
    type: "task.started",
    at: "2026-11-23T08:41:31Z",
    actor: "worker-01",
    detail: "search-web attempt 1 accepted",
  },
  {
    id: "evt_7",
    type: "task.succeeded",
    at: "2026-11-23T08:41:26Z",
    actor: "worker-01",
    detail: "plan-questions produced 6 questions",
  },
  {
    id: "evt_6",
    type: "task.started",
    at: "2026-11-23T08:41:21Z",
    actor: "worker-01",
    detail: "plan-questions attempt 1 accepted",
  },
  {
    id: "evt_5",
    type: "task.succeeded",
    at: "2026-11-23T08:41:19Z",
    actor: "worker-02",
    detail: "collect-source read 3 documents",
  },
  {
    id: "evt_4",
    type: "execution.started",
    at: "2026-11-23T08:41:00Z",
    actor: "kushal@local",
    detail: "execution moved pending → running",
  },
];

export async function getDemoExecutions(): Promise<Execution[]> {
  return demoExecutions;
}

export function findDemoExecution(id: string): Execution | undefined {
  return demoExecutions.find((execution) => execution.id === id);
}
