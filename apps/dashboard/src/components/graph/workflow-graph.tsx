"use client";

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
import {
  Bot,
  Braces,
  CircleStop,
  Clock,
  GitBranch,
  Merge,
  Play,
  ShieldCheck,
  Split,
  Wrench,
} from "lucide-react";
import { useMemo } from "react";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusIndicator } from "@/components/ui/status-indicator";
import type {
  Task,
  WorkflowEdge,
  WorkflowGraph as WorkflowGraphType,
  WorkflowNode,
} from "@/lib/api/types";
import { cn, formatDuration } from "@/lib/utils";

type GraphNodeData = {
  label: string;
  nodeType: string;
  status: string;
  durationMs: number | null;
  selected: boolean;
  summary: string;
};

type FlowNode = Node<GraphNodeData & Record<string, unknown>, "workflow">;

const NODE_ICON: Record<string, typeof Bot> = {
  start: Play,
  end: CircleStop,
  agent: Bot,
  llm: Bot,
  tool: Wrench,
  condition: GitBranch,
  transform: Braces,
  parallel: Split,
  join: Merge,
  delay: Clock,
  approval: ShieldCheck,
  subworkflow: GitBranch,
  loop: GitBranch,
};

function layout(nodes: WorkflowNode[], edges: WorkflowEdge[]) {
  const incoming = new Map<string, string[]>();
  for (const node of nodes) incoming.set(node.id, []);
  for (const edge of edges) incoming.get(edge.to)?.push(edge.from);
  const levels = new Map<string, number>();
  const queue = nodes
    .filter((node) => (incoming.get(node.id)?.length ?? 0) === 0)
    .map((node) => node.id);
  for (const id of queue) levels.set(id, 0);
  let index = 0;
  while (index < queue.length) {
    const id = queue[index++];
    if (id === undefined) break;
    const level = levels.get(id) ?? 0;
    for (const edge of edges.filter((item) => item.from === id)) {
      const next = Math.max(levels.get(edge.to) ?? 0, level + 1);
      levels.set(edge.to, next);
      if (!queue.includes(edge.to)) queue.push(edge.to);
    }
  }
  const byLevel = new Map<number, string[]>();
  nodes.forEach((node, position) => {
    const level = levels.get(node.id) ?? position;
    byLevel.set(level, [...(byLevel.get(level) ?? []), node.id]);
  });
  const positions = new Map<string, { x: number; y: number }>();
  for (const [level, ids] of byLevel) {
    ids.forEach((id, row) => {
      positions.set(id, { x: level * 280, y: row * 130 });
    });
  }
  return positions;
}

function nodeSummary(node: WorkflowNode): string {
  if (node.agent) return `agent ${node.agent.agent_id.slice(0, 12)}… v${node.agent.version}`;
  if (node.tool) return `tool ${node.tool.tool_id} v${node.tool.version}`;
  if (node.config?.expression) return `when ${String(node.config.expression)}`;
  if (node.config?.delay) return "durable wait";
  if (node.type === "approval") return "human decision";
  if (node.type === "join") return "fan-in";
  if (node.type === "parallel") return "fan-out";
  return node.type;
}

function WorkflowNodeCard({ data }: NodeProps<FlowNode>) {
  const Icon = NODE_ICON[data.nodeType] ?? Braces;
  return (
    <div
      className={cn(
        "w-56 rounded-lg border bg-card p-3 shadow-sm transition-shadow",
        data.selected ? "border-primary ring-2 ring-primary/20" : "border-border",
      )}
    >
      <Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-border" />
      <div className="flex items-center justify-between gap-2 text-[10px] uppercase tracking-wide text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <Icon className="size-3.5" aria-hidden="true" />
          {data.nodeType}
        </span>
        <span className="font-mono normal-case">{data.label}</span>
      </div>
      <p className="mt-1.5 truncate text-[11px] text-muted-foreground" title={data.summary}>
        {data.summary}
      </p>
      <div className="mt-2 flex items-center justify-between gap-2">
        <StatusIndicator status={data.status} />
        <span className="text-[10px] text-muted-foreground">
          {data.durationMs === null ? "—" : formatDuration(data.durationMs)}
        </span>
      </div>
      <Handle type="source" position={Position.Right} className="!size-2 !border-0 !bg-border" />
    </div>
  );
}

const nodeTypes = { workflow: WorkflowNodeCard };

export function WorkflowGraph({
  graph,
  tasks = [],
  selectedNodeId,
  onSelectNode,
  className,
  showStatus = true,
}: {
  graph: WorkflowGraphType | undefined;
  tasks?: Task[];
  selectedNodeId?: string | null;
  onSelectNode?: (nodeId: string) => void;
  className?: string;
  showStatus?: boolean;
}) {
  const taskByNode = useMemo(() => new Map(tasks.map((task) => [task.node_id, task])), [tasks]);
  const positions = useMemo(() => layout(graph?.nodes ?? [], graph?.edges ?? []), [graph]);
  const nodes: FlowNode[] = useMemo(
    () =>
      (graph?.nodes ?? []).map((node) => {
        const task = taskByNode.get(node.id);
        const duration =
          task?.started_at && task.ended_at
            ? new Date(task.ended_at).getTime() - new Date(task.started_at).getTime()
            : null;
        return {
          id: node.id,
          type: "workflow",
          position: positions.get(node.id) ?? { x: 0, y: 0 },
          data: {
            label: node.id,
            nodeType: node.type,
            status: showStatus ? (task?.state ?? "pending") : "published",
            durationMs: duration,
            selected: selectedNodeId === node.id,
            summary: nodeSummary(node),
          },
        };
      }),
    [graph?.nodes, positions, selectedNodeId, showStatus, taskByNode],
  );
  const edges: Edge[] = useMemo(
    () =>
      (graph?.edges ?? []).map((edge, index) => ({
        id: `${edge.from}-${edge.to}-${index}`,
        source: edge.from,
        target: edge.to,
        label: edge.when ? "conditional" : edge.data_mapping ? "mapped" : undefined,
        type: "smoothstep",
        animated: taskByNode.get(edge.from)?.state === "running",
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.55 },
        labelStyle: { fill: "var(--color-muted-foreground)", fontSize: 10 },
        labelBgStyle: { fill: "var(--color-card)" },
      })),
    [graph?.edges, taskByNode],
  );

  if (!graph || graph.nodes.length === 0) {
    return (
      <EmptyState title="No graph" description="This workflow version has no node definition." />
    );
  }

  return (
    <div className={className ?? "h-[460px] w-full"}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        onNodeClick={(_, node) => onSelectNode?.(node.id)}
        panOnScroll
        minZoom={0.25}
        className="rounded-lg border border-border bg-card"
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
