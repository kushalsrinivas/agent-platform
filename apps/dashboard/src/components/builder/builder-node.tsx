"use client";

import { Handle, type NodeProps, Position } from "@xyflow/react";
import {
  Bot,
  Braces,
  CircleAlert,
  CircleStop,
  Clock3,
  GitBranch,
  Merge,
  Play,
  Repeat,
  ShieldCheck,
  Split,
  Workflow,
  Wrench,
} from "lucide-react";

import { StatusIndicator } from "@/components/ui/status-indicator";
import type { BuilderNode } from "@/lib/builder/graph";
import { nodeSummary } from "@/lib/builder/graph";
import { cn, formatDuration } from "@/lib/utils";

export const NODE_ICON: Record<string, typeof Bot> = {
  start: Play,
  end: CircleStop,
  agent: Bot,
  llm: Bot,
  tool: Wrench,
  condition: GitBranch,
  transform: Braces,
  parallel: Split,
  join: Merge,
  delay: Clock3,
  approval: ShieldCheck,
  subworkflow: Workflow,
  loop: Repeat,
};

export function nodeTypeLabel(type: string): string {
  switch (type) {
    case "llm":
      return "LLM";
    case "subworkflow":
      return "Subworkflow";
    default:
      return type.charAt(0).toUpperCase() + type.slice(1);
  }
}

export function BuilderNodeCard({ data, selected }: NodeProps<BuilderNode>) {
  const node = data.node;
  const Icon = NODE_ICON[node.type] ?? Braces;
  const failed = data.executionState === "failed" || data.hasError;
  return (
    <div
      className={cn(
        "w-60 rounded-lg border bg-card p-3 shadow-sm transition-shadow",
        failed
          ? "border-danger/60"
          : data.executionState === "running"
            ? "border-info/70"
            : selected
              ? "border-primary ring-2 ring-primary/25"
              : "border-border",
      )}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="!size-2.5 !border-2 !border-card !bg-muted-foreground"
      />
      <div className="flex items-center justify-between gap-2 text-[10px] uppercase tracking-wide text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <Icon className="size-3.5" aria-hidden="true" />
          {nodeTypeLabel(node.type)}
        </span>
        {data.hasError ? (
          <CircleAlert className="size-3.5 text-danger" aria-label="Validation error" />
        ) : null}
      </div>
      <p className="mt-1.5 truncate font-mono text-xs font-medium" title={node.id}>
        {node.id || "unnamed"}
      </p>
      <p className="mt-0.5 truncate text-[11px] text-muted-foreground" title={nodeSummary(node)}>
        {nodeSummary(node)}
      </p>
      {data.executionState ? (
        <div className="mt-2 flex items-center justify-between gap-2 border-t border-border pt-2">
          <StatusIndicator status={data.executionState} />
          <span className="text-[10px] text-muted-foreground">
            {data.durationMs === null || data.durationMs === undefined
              ? "—"
              : formatDuration(data.durationMs)}
          </span>
        </div>
      ) : null}
      <Handle
        type="source"
        position={Position.Right}
        className="!size-2.5 !border-2 !border-card !bg-muted-foreground"
      />
    </div>
  );
}
