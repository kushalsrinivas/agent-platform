import { z } from "zod";

export const executionStatusSchema = z.enum([
  "queued",
  "running",
  "succeeded",
  "failed",
  "cancelled",
]);
export type ExecutionStatus = z.infer<typeof executionStatusSchema>;

export const nodeKindSchema = z.enum(["agent", "tool"]);
export type NodeKind = z.infer<typeof nodeKindSchema>;

export const executionSchema = z.object({
  id: z.string().min(1),
  workflowName: z.string().min(1),
  workflowVersion: z.number().int().positive(),
  status: executionStatusSchema,
  startedAt: z.iso.datetime(),
  durationMs: z.number().int().nonnegative(),
  triggeredBy: z.string().min(1),
  input: z.record(z.string(), z.unknown()),
  output: z.record(z.string(), z.unknown()).nullable(),
});
export type Execution = z.infer<typeof executionSchema>;

export const graphNodeSchema = z.object({
  id: z.string(),
  label: z.string(),
  kind: nodeKindSchema,
  status: executionStatusSchema,
  durationMs: z.number().int().nonnegative().optional(),
});
export type GraphNodeData = z.infer<typeof graphNodeSchema>;

export const graphEdgeSchema = z.object({
  id: z.string(),
  source: z.string(),
  target: z.string(),
  label: z.string().optional(),
});
export type GraphEdge = z.infer<typeof graphEdgeSchema>;

export const workflowSchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.number().int().positive(),
  status: z.enum(["draft", "published", "deprecated"]),
  nodeCount: z.number().int().positive(),
  updatedAt: z.iso.datetime(),
});
export type Workflow = z.infer<typeof workflowSchema>;

/** The client-side shape of the new-execution form. */
export const newExecutionSchema = z.object({
  workflow: z.string().min(1, "Choose a workflow"),
  input: z
    .string()
    .min(1, "Input is required")
    .transform((value, ctx) => {
      try {
        const parsed: unknown = JSON.parse(value);
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          ctx.addIssue({ code: "custom", message: "Input must be a JSON object" });
          return z.NEVER;
        }
        return parsed as Record<string, unknown>;
      } catch {
        ctx.addIssue({ code: "custom", message: "Input must be valid JSON" });
        return z.NEVER;
      }
    }),
});
