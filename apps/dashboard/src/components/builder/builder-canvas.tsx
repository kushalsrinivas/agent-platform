"use client";

import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  type OnConnect,
  type OnEdgesChange,
  type OnNodesChange,
  ReactFlow,
  useReactFlow,
} from "@xyflow/react";
import { useEffect } from "react";

import { BuilderNodeCard, NODE_ICON } from "@/components/builder/builder-node";
import type { BuilderEdge, BuilderNode } from "@/lib/builder/graph";

const nodeTypes = { builder: BuilderNodeCard };

export function BuilderCanvas({
  nodes,
  edges,
  onNodesChange,
  onEdgesChange,
  onConnect,
  onDropNode,
  onNodeDragStart,
  onNodeDragStop,
  onSelectionChange,
  focusNodeId,
  focusToken,
  fitToken,
}: {
  nodes: BuilderNode[];
  edges: BuilderEdge[];
  onNodesChange: OnNodesChange<BuilderNode>;
  onEdgesChange: OnEdgesChange<BuilderEdge>;
  onConnect: OnConnect;
  onDropNode: (type: string, position: { x: number; y: number }) => void;
  onNodeDragStart: () => void;
  onNodeDragStop: () => void;
  onSelectionChange: (nodeIds: string[], edgeIds: string[]) => void;
  focusNodeId?: string;
  focusToken: number;
  fitToken: number;
}) {
  const flow = useReactFlow();

  useEffect(() => {
    if (!focusNodeId) return;
    void focusToken;
    const timer = setTimeout(() => {
      void flow.fitView({
        nodes: [{ id: focusNodeId }],
        duration: 350,
        maxZoom: 1.35,
        padding: 1.6,
      });
    }, 80);
    return () => clearTimeout(timer);
  }, [flow, focusNodeId, focusToken]);

  useEffect(() => {
    if (fitToken === 0) return;
    const timer = setTimeout(() => {
      void flow.fitView({ duration: 350, padding: 0.2 });
    }, 80);
    return () => clearTimeout(timer);
  }, [fitToken, flow]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      onNodeDragStart={onNodeDragStart}
      onNodeDragStop={onNodeDragStop}
      onSelectionChange={({ nodes: selected, edges: selectedEdges }) =>
        onSelectionChange(
          selected.map((node) => node.id),
          selectedEdges.map((edge) => edge.id),
        )
      }
      onDragOver={(event) => {
        if (event.dataTransfer.types.includes("application/reactflow")) {
          event.preventDefault();
          event.dataTransfer.dropEffect = "move";
        }
      }}
      onDrop={(event) => {
        event.preventDefault();
        const type = event.dataTransfer.getData("application/reactflow");
        if (!type) return;
        onDropNode(type, flow.screenToFlowPosition({ x: event.clientX, y: event.clientY }));
      }}
      fitView
      fitViewOptions={{ padding: 0.2 }}
      minZoom={0.15}
      maxZoom={2.5}
      defaultEdgeOptions={{
        type: "smoothstep",
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
        labelStyle: { fill: "var(--color-muted-foreground)", fontSize: 10 },
        labelBgStyle: { fill: "var(--color-card)" },
      }}
      deleteKeyCode={null}
      multiSelectionKeyCode={["Meta", "Control", "Shift"]}
      selectionKeyCode="Shift"
      panOnScroll
      className="rounded-lg border border-border bg-card"
      proOptions={{ hideAttribution: true }}
    >
      <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
      <Controls showInteractive={false} />
      <MiniMap
        pannable
        zoomable
        className="!bg-card"
        nodeColor={(node) => {
          const data = node.data as BuilderNode["data"] | undefined;
          const type = data?.node?.type ?? "";
          if (data?.executionState === "failed" || data?.hasError) return "var(--color-danger)";
          if (data?.executionState === "running") return "var(--color-info)";
          if (data?.executionState === "succeeded") return "var(--color-success)";
          if (type === "start" || type === "end") return "var(--color-muted-foreground)";
          return "var(--color-primary)";
        }}
        maskColor="color-mix(in oklab, var(--color-background) 70%, transparent)"
      />
    </ReactFlow>
  );
}

export function nodeIcon(type: string) {
  return NODE_ICON[type];
}
