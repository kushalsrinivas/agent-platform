import {
  Background,
  BackgroundVariant,
  Controls,
  type Edge,
  Handle,
  MarkerType,
  type Node,
  type NodeProps,
  Position,
  ReactFlow,
} from "@xyflow/react";
import { Bot, Wrench } from "lucide-react";

import { StatusIndicator } from "@/components/ui/status-indicator";
import { demoGraphEdges, demoGraphNodes } from "@/lib/demo-data";
import type { GraphNodeData } from "@/lib/schemas";
import { formatDuration } from "@/lib/utils";

type WorkflowNode = Node<GraphNodeData & Record<string, unknown>, "workflow">;

const POSITIONS: Record<string, { x: number; y: number }> = {
  collect: { x: 0, y: 120 },
  plan: { x: 260, y: 120 },
  search: { x: 520, y: 120 },
  synthesize: { x: 780, y: 40 },
  publish: { x: 1040, y: 40 },
};

const nodes: WorkflowNode[] = demoGraphNodes.map((node) => ({
  id: node.id,
  type: "workflow",
  position: POSITIONS[node.id] ?? { x: 0, y: 0 },
  data: node,
}));

const edges: Edge[] = demoGraphEdges.map((edge) => ({
  id: edge.id,
  source: edge.source,
  target: edge.target,
  label: edge.label,
  type: "smoothstep",
  animated: edge.target === "synthesize",
  markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
  style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
  labelStyle: { fill: "var(--color-muted-foreground)", fontSize: 11 },
  labelBgStyle: { fill: "var(--color-card)" },
}));

function WorkflowNodeCard({ data }: NodeProps<WorkflowNode>) {
  const Icon = data.kind === "agent" ? Bot : Wrench;
  const active = data.status === "running";

  return (
    <div
      className={`w-52 rounded-lg border bg-card p-3 shadow-sm ${
        active ? "border-info/60 ring-2 ring-info/20" : "border-border"
      }`}
    >
      <Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-border" />
      <div className="flex items-center gap-2 text-[11px] uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" aria-hidden="true" />
        {data.kind}
      </div>
      <p className="mt-1 truncate font-mono text-[13px] font-medium">{data.label}</p>
      <div className="mt-2 flex items-center justify-between gap-2">
        <StatusIndicator status={data.status} />
        <span className="text-[11px] text-muted-foreground">
          {data.durationMs === undefined ? "—" : formatDuration(data.durationMs)}
        </span>
      </div>
      <Handle type="source" position={Position.Right} className="!size-2 !border-0 !bg-border" />
    </div>
  );
}

const nodeTypes = { workflow: WorkflowNodeCard };

export function WorkflowGraph({ className }: { className?: string }) {
  return (
    <div className={className ?? "h-[420px] w-full"}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnScroll
        minZoom={0.4}
        className="rounded-lg border border-border bg-card"
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
