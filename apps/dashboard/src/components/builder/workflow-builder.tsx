"use client";

import { useQueryClient } from "@tanstack/react-query";
import {
  addEdge,
  type Connection,
  MarkerType,
  type OnEdgesChange,
  type OnNodesChange,
  ReactFlowProvider,
} from "@xyflow/react";
import {
  ArrowLeft,
  LayoutGrid,
  Play,
  Redo2,
  Rocket,
  Save,
  ShieldCheck,
  Undo2,
  Workflow as WorkflowIcon,
} from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";

import { BuilderCanvas } from "@/components/builder/builder-canvas";
import { ExecutionPanel } from "@/components/builder/execution-panel";
import { NodeInspector } from "@/components/builder/node-inspector";
import { createNode, NodeLibrary } from "@/components/builder/node-library";
import { RunDialog } from "@/components/builder/run-dialog";
import { ValidationPanel } from "@/components/builder/validation-panel";
import { VersionPanel } from "@/components/builder/version-panel";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { TabPanel, Tabs } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { ApiError, api, apiPaths } from "@/lib/api";
import {
  useExecutionTasks,
  useWorkflow,
  useWorkflowVersion,
  useWorkflowVersions,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import type {
  Workflow,
  WorkflowEdge,
  WorkflowGraph,
  WorkflowNode,
  WorkflowVersion,
} from "@/lib/api/types";
import {
  applyLayout,
  type BuilderEdge,
  type BuilderNode,
  dedupeIssues,
  edgeId,
  localValidate,
  nextNodeId,
  serializeGraph,
  serverIssues,
  stableStringify,
  toCanvas,
  type ValidationIssue,
} from "@/lib/builder/graph";
import { cn } from "@/lib/utils";

type Snapshot = { nodes: BuilderNode[]; edges: BuilderEdge[] };
type GraphMeta = Pick<WorkflowGraph, "policy" | "input_schema">;

function cloneSnapshot(snapshot: Snapshot): Snapshot {
  return structuredClone(snapshot);
}

export function WorkflowBuilder({ workflowId }: { workflowId?: string }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const { orgId, projectId } = useWorkspaceScope();
  const workflow = useWorkflow(orgId, projectId, workflowId ?? "");
  const versionsQuery = useWorkflowVersions(orgId, projectId, workflowId ?? "");
  const [loadVersion, setLoadVersion] = useState(0);
  const sourceVersion = useWorkflowVersion(orgId, projectId, workflowId ?? "", loadVersion);

  const [nodes, setNodes] = useState<BuilderNode[]>([]);
  const [edges, setEdges] = useState<BuilderEdge[]>([]);
  const [meta, setMeta] = useState<GraphMeta>({});
  const [initialized, setInitialized] = useState(false);
  const [currentVersion, setCurrentVersion] = useState(0);
  const [deployedVersion, setDeployedVersion] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [sidebar, setSidebar] = useState("configure");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [activating, setActivating] = useState<number | null>(null);
  const [validating, setValidating] = useState(false);
  const [valid, setValid] = useState<boolean | null>(null);
  const [serverProblems, setServerProblems] = useState<ValidationIssue[]>([]);
  const [runOpen, setRunOpen] = useState(false);
  const [executionId, setExecutionId] = useState("");
  const [focusNodeId, setFocusNodeId] = useState<string | undefined>();
  const [focusToken, setFocusToken] = useState(0);
  const [fitToken, setFitToken] = useState(0);
  const [, setHistoryTick] = useState(0);

  const nodesRef = useRef(nodes);
  const edgesRef = useRef(edges);
  const metaRef = useRef(meta);
  const baseline = useRef("");
  const history = useRef<{
    past: Snapshot[];
    future: Snapshot[];
    lastKey: string;
    lastAt: number;
  }>({ past: [], future: [], lastKey: "", lastAt: 0 });
  const dragSnapshot = useRef<Snapshot | null>(null);
  const clipboard = useRef<Snapshot | null>(null);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);
  useEffect(() => {
    edgesRef.current = edges;
  }, [edges]);
  useEffect(() => {
    metaRef.current = meta;
  }, [meta]);

  useEffect(() => {
    if (!workflowId) return;
    if (workflow.data && loadVersion === 0) setLoadVersion(workflow.data.latest_version);
  }, [loadVersion, workflow.data, workflowId]);

  useEffect(() => {
    if (workflowId) {
      setDeployedVersion(workflow.data?.deployed_version ?? null);
    }
  }, [workflow.data?.deployed_version, workflowId]);

  useEffect(() => {
    if (initialized) return;
    if (!workflowId) {
      const start: BuilderNode = {
        id: "start_1",
        type: "builder",
        position: { x: 0, y: 0 },
        data: { node: createNode("start", "start_1") },
      };
      const end: BuilderNode = {
        id: "end_1",
        type: "builder",
        position: { x: 300, y: 0 },
        data: { node: createNode("end", "end_1") },
      };
      const initialEdges: BuilderEdge[] = [
        {
          id: edgeId("start_1", "end_1"),
          source: "start_1",
          target: "end_1",
          type: "smoothstep",
          markerEnd: { type: MarkerType.ArrowClosed },
        },
      ];
      setNodes([start, end]);
      setEdges(initialEdges);
      baseline.current = stableStringify(serializeGraph([start, end], initialEdges, {}));
      setInitialized(true);
      return;
    }
    if (!sourceVersion.data) return;
    const canvas = toCanvas(sourceVersion.data.graph);
    setNodes(canvas.nodes);
    setEdges(canvas.edges);
    setMeta({
      policy: sourceVersion.data.graph.policy,
      input_schema: sourceVersion.data.graph.input_schema,
    });
    setCurrentVersion(sourceVersion.data.version);
    baseline.current = stableStringify(sourceVersion.data.graph);
    setDirty(false);
    setInitialized(true);
  }, [initialized, sourceVersion.data, workflowId]);

  const graph = useMemo(() => serializeGraph(nodes, edges, meta), [edges, meta, nodes]);
  const _graphSignature = useMemo(() => stableStringify(graph), [graph]);
  const localIssues = useMemo(() => localValidate(graph), [graph]);
  const issues = useMemo(
    () => dedupeIssues([...localIssues, ...serverProblems]),
    [localIssues, serverProblems],
  );
  const errorNodeIds = useMemo(
    () =>
      new Set(
        issues.map((issue) => issue.nodeId).filter((value): value is string => Boolean(value)),
      ),
    [issues],
  );

  const tasks = useExecutionTasks(orgId, projectId, executionId);
  const taskByNode = useMemo(
    () => new Map((tasks.data?.items ?? []).map((task) => [task.node_id, task])),
    [tasks.data],
  );

  const displayNodes = useMemo(
    () =>
      nodes.map((node) => {
        const task = taskByNode.get(node.data.node.id);
        const duration =
          task?.started_at && task.ended_at
            ? new Date(task.ended_at).getTime() - new Date(task.started_at).getTime()
            : task?.started_at
              ? Date.now() - new Date(task.started_at).getTime()
              : null;
        return {
          ...node,
          data: {
            ...node.data,
            hasError: errorNodeIds.has(node.data.node.id),
            executionState: task?.state,
            durationMs: duration,
          },
        };
      }),
    [errorNodeIds, nodes, taskByNode],
  );

  const selectedNodeIds = useMemo(
    () => nodes.filter((node) => node.selected).map((node) => node.id),
    [nodes],
  );
  const selectedEdgeIds = useMemo(
    () => edges.filter((edge) => edge.selected).map((edge) => edge.id),
    [edges],
  );
  const selectedNode = nodes.find((node) => node.id === selectedNodeIds[0])?.data.node ?? null;
  const upstreamIds = useMemo(() => {
    if (!selectedNode) return [];
    const incoming = new Map<string, string[]>();
    for (const node of graph.nodes) incoming.set(node.id, []);
    for (const edge of graph.edges) incoming.get(edge.to)?.push(edge.from);
    const result = new Set<string>();
    const queue = [...(incoming.get(selectedNode.id) ?? [])];
    while (queue.length > 0) {
      const id = queue.shift();
      if (id === undefined || result.has(id)) continue;
      result.add(id);
      queue.push(...(incoming.get(id) ?? []));
    }
    return [...result];
  }, [graph.edges, graph.nodes, selectedNode]);

  const versions = useMemo(
    () =>
      [...(versionsQuery.data?.items ?? [])].sort((left, right) => left.version - right.version),
    [versionsQuery.data],
  );

  useEffect(() => {
    setDirty(initialized && stableStringify(graph) !== baseline.current);
  }, [graph, initialized]);

  useEffect(() => {
    if (initialized) setFitToken((token) => token + 1);
  }, [initialized]);

  useEffect(() => {
    setServerProblems([]);
    setValid(null);
  }, []);

  function currentSnapshot(): Snapshot {
    return cloneSnapshot({ nodes: nodesRef.current, edges: edgesRef.current });
  }

  function pushHistory(key: string, snapshot?: Snapshot) {
    const now = Date.now();
    if (!snapshot && history.current.lastKey === key && now - history.current.lastAt < 800) {
      history.current.lastAt = now;
      return;
    }
    history.current.past.push(snapshot ?? currentSnapshot());
    if (history.current.past.length > 100) history.current.past.shift();
    history.current.future = [];
    history.current.lastKey = key;
    history.current.lastAt = now;
    setHistoryTick((tick) => tick + 1);
  }

  function restore(snapshot: Snapshot) {
    setNodes(cloneSnapshot(snapshot).nodes);
    setEdges(cloneSnapshot(snapshot).edges);
  }

  function undo() {
    const previous = history.current.past.pop();
    if (!previous) return;
    history.current.future.push(currentSnapshot());
    history.current.lastKey = "";
    restore(previous);
    setHistoryTick((tick) => tick + 1);
  }

  function redo() {
    const next = history.current.future.pop();
    if (!next) return;
    history.current.past.push(currentSnapshot());
    history.current.lastKey = "";
    restore(next);
    setHistoryTick((tick) => tick + 1);
  }

  function onNodesChange(changes: Parameters<OnNodesChange<BuilderNode>>[0]) {
    setNodes((current) => changes.applyTo(current));
  }

  function onEdgesChange(changes: Parameters<OnEdgesChange<BuilderEdge>>[0]) {
    setEdges((current) => changes.applyTo(current));
  }

  function connect(connection: Connection) {
    if (!connection.source || !connection.target || connection.source === connection.target) return;
    if (
      edgesRef.current.some(
        (edge) => edge.source === connection.source && edge.target === connection.target,
      )
    ) {
      return;
    }
    const source = nodesRef.current.find((node) => node.id === connection.source)?.data.node;
    const outgoing = edgesRef.current.filter((edge) => edge.source === connection.source).length;
    const when =
      source?.type === "condition"
        ? outgoing === 0
          ? `outputs.${connection.source}.result == true`
          : outgoing === 1
            ? `outputs.${connection.source}.result == false`
            : undefined
        : undefined;
    pushHistory("connect");
    const next = addEdge<BuilderEdge>(
      {
        ...connection,
        id: edgeId(connection.source, connection.target),
        type: "smoothstep",
        label: when,
        data: { when },
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
        labelStyle: { fill: "var(--color-muted-foreground)", fontSize: 10 },
        labelBgStyle: { fill: "var(--color-card)" },
      },
      edgesRef.current,
    );
    setEdges(next);
  }

  function addNode(type: string, position?: { x: number; y: number }) {
    const id = nextNodeId(type, nodesRef.current);
    const selected = nodesRef.current.find((node) => node.id === selectedNodeIds[0]);
    const spot =
      position ??
      (selected
        ? { x: selected.position.x, y: selected.position.y + 170 }
        : { x: nodesRef.current.length * 35, y: nodesRef.current.length * 25 });
    const node: BuilderNode = {
      id,
      type: "builder",
      position: spot,
      selected: true,
      data: { node: createNode(type, id), hasError: false },
    };
    pushHistory("add-node");
    let nextNodes = [...nodesRef.current.map((item) => ({ ...item, selected: false })), node];
    const direct =
      type !== "start" &&
      type !== "end" &&
      edgesRef.current.length === 1 &&
      nodesRef.current.length === 2 &&
      nodesRef.current.some(
        (item) => item.data.node.type === "start" && item.id === edgesRef.current[0]?.source,
      ) &&
      nodesRef.current.some(
        (item) => item.data.node.type === "end" && item.id === edgesRef.current[0]?.target,
      );
    if (direct && edgesRef.current[0]) {
      const from = edgesRef.current[0].source;
      const to = edgesRef.current[0].target;
      const nextEdges: BuilderEdge[] = [
        {
          id: edgeId(from, id),
          source: from,
          target: id,
          type: "smoothstep",
          markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
          style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
        },
        {
          id: edgeId(id, to),
          source: id,
          target: to,
          type: "smoothstep",
          markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
          style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
        },
      ];
      nextNodes = applyLayout(nextNodes, nextEdges);
      setNodes(nextNodes);
      setEdges(nextEdges);
    } else {
      setNodes(nextNodes);
      setEdges(edgesRef.current.map((edge) => ({ ...edge, selected: false })));
    }
    setSidebar("configure");
  }

  function removeNodes(ids: string[]) {
    if (ids.length === 0) return;
    pushHistory("delete-nodes");
    const removed = new Set(ids);
    setNodes(nodesRef.current.filter((node) => !removed.has(node.id)));
    setEdges(
      edgesRef.current.filter((edge) => !removed.has(edge.source) && !removed.has(edge.target)),
    );
  }

  function removeEdges(ids: string[]) {
    if (ids.length === 0) return;
    pushHistory("delete-edges");
    const removed = new Set(ids);
    setEdges(edgesRef.current.filter((edge) => !removed.has(edge.id)));
  }

  function deleteSelection() {
    if (selectedNodeIds.length > 0) removeNodes(selectedNodeIds);
    else if (selectedEdgeIds.length > 0) removeEdges(selectedEdgeIds);
  }

  function updateNode(next: WorkflowNode) {
    if (!selectedNode) return;
    if (next.id !== selectedNode.id) {
      next = { ...next, id: selectedNode.id };
    }
    pushHistory(`node:${next.id}`);
    setNodes((current) =>
      current.map((node) =>
        node.id === next.id ? { ...node, data: { ...node.data, node: next } } : node,
      ),
    );
  }

  function renameNode(from: string, to: string) {
    if (from === to) return;
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(to) || nodesRef.current.some((node) => node.id === to)) {
      toast.error("Node id is invalid or already used.");
      return;
    }
    const rewrite = (value: string) =>
      value.replace(new RegExp(`outputs\\.${from}(?![A-Za-z0-9_])`, "g"), `outputs.${to}`);
    pushHistory("rename-node");
    setNodes((current) =>
      current.map((item) => {
        const node = item.data.node;
        const config = node.config;
        const nextConfig = config
          ? {
              ...config,
              expression: config.expression ? rewrite(config.expression) : config.expression,
              mapping: config.mapping
                ? Object.fromEntries(
                    Object.entries(config.mapping).map(([key, value]) => [key, rewrite(value)]),
                  )
                : config.mapping,
              input: config.input
                ? Object.fromEntries(
                    Object.entries(config.input).map(([key, value]) => [key, rewrite(value)]),
                  )
                : config.input,
              loop: config.loop?.exit_when
                ? { ...config.loop, exit_when: rewrite(config.loop.exit_when) }
                : config.loop,
              output_from: config.output_from?.map((ref) => (ref === from ? to : ref)),
            }
          : config;
        return {
          ...item,
          id: item.id === from ? to : item.id,
          data: {
            ...item.data,
            node: {
              ...node,
              id: node.id === from ? to : node.id,
              config: nextConfig,
              input_from: node.input_from?.map((ref) => (ref === from ? to : ref)),
            },
          },
        };
      }),
    );
    setEdges((current) =>
      current.map((edge) => ({
        ...edge,
        id:
          edge.source === from || edge.target === from
            ? edgeId(
                edge.source === from ? to : edge.source,
                edge.target === from ? to : edge.target,
              )
            : edge.id,
        source: edge.source === from ? to : edge.source,
        target: edge.target === from ? to : edge.target,
        data: edge.data
          ? {
              ...edge.data,
              when: edge.data.when ? rewrite(edge.data.when) : edge.data.when,
              dataMapping: edge.data.dataMapping
                ? Object.fromEntries(
                    Object.entries(edge.data.dataMapping).map(([key, value]) => [
                      key,
                      rewrite(value),
                    ]),
                  )
                : edge.data.dataMapping,
            }
          : edge.data,
        label: edge.data?.when ? rewrite(edge.data.when) : edge.label,
      })),
    );
  }

  function addEdgeManually(from: string, to: string, when?: string) {
    if (from === to) return;
    if (edgesRef.current.some((edge) => edge.source === from && edge.target === to)) return;
    pushHistory("add-edge");
    setEdges([
      ...edgesRef.current,
      {
        id: edgeId(from, to),
        source: from,
        target: to,
        type: "smoothstep",
        label: when,
        data: { when },
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        style: { stroke: "var(--color-muted-foreground)", strokeWidth: 1.5, opacity: 0.6 },
        labelStyle: { fill: "var(--color-muted-foreground)", fontSize: 10 },
        labelBgStyle: { fill: "var(--color-card)" },
      },
    ]);
  }

  function patchEdge(from: string, to: string, patch: Partial<WorkflowEdge>) {
    pushHistory(`edge:${from}:${to}`);
    setEdges((current) =>
      current.map((edge) => {
        if (edge.source !== from || edge.target !== to) return edge;
        const data = { ...edge.data };
        if ("when" in patch) data.when = patch.when;
        if ("data_mapping" in patch) data.dataMapping = patch.data_mapping;
        if ("priority" in patch) data.priority = patch.priority;
        return { ...edge, data, label: data.when || undefined };
      }),
    );
  }

  function removeEdge(from: string, to: string) {
    pushHistory("remove-edge");
    setEdges(edgesRef.current.filter((edge) => !(edge.source === from && edge.target === to)));
  }

  function autoLayout() {
    pushHistory("layout");
    setNodes((current) => applyLayout(current, edgesRef.current));
    setFitToken((token) => token + 1);
  }

  function copySelection() {
    const ids = new Set(selectedNodeIds);
    if (ids.size === 0) return;
    clipboard.current = cloneSnapshot({
      nodes: nodesRef.current.filter((node) => ids.has(node.id)),
      edges: edgesRef.current.filter((edge) => ids.has(edge.source) && ids.has(edge.target)),
    });
    toast.success(`${ids.size} node${ids.size === 1 ? "" : "s"} copied`);
  }

  function pasteSelection() {
    const copied = clipboard.current;
    if (!copied) return;
    pushHistory("paste");
    const existing = nodesRef.current.map((node) => ({ id: node.id }));
    const idMap = new Map<string, string>();
    const pastedNodes = copied.nodes.map((node) => {
      const id = nextNodeId(node.data.node.type, existing);
      existing.push({ id });
      idMap.set(node.id, id);
      return {
        ...node,
        id,
        position: { x: node.position.x + 50, y: node.position.y + 50 },
        selected: true,
        data: { ...node.data, node: { ...structuredClone(node.data.node), id } },
      };
    });
    const pastedEdges = copied.edges.map((edge) => ({
      ...edge,
      id: edgeId(idMap.get(edge.source) ?? edge.source, idMap.get(edge.target) ?? edge.target),
      source: idMap.get(edge.source) ?? edge.source,
      target: idMap.get(edge.target) ?? edge.target,
    }));
    setNodes([...nodesRef.current.map((node) => ({ ...node, selected: false })), ...pastedNodes]);
    setEdges([...edgesRef.current, ...pastedEdges]);
  }

  function focusOnNode(nodeId: string) {
    setNodes((current) => current.map((node) => ({ ...node, selected: node.id === nodeId })));
    setEdges((current) => current.map((edge) => ({ ...edge, selected: false })));
    setSidebar("configure");
    setFocusNodeId(nodeId);
    setFocusToken((token) => token + 1);
  }

  async function validateGraph() {
    if (!orgId || !projectId) return;
    setValidating(true);
    try {
      await api(apiPaths.validateWorkflow(orgId, projectId), {
        method: "POST",
        json: { graph },
      });
      setServerProblems([]);
      setValid(true);
      toast.success("Workflow graph is valid");
    } catch (error) {
      setValid(false);
      const problems = serverIssues(error, graph);
      setServerProblems(problems);
      toast.error(
        problems.length > 0
          ? `Graph has ${problems.length} problem${problems.length === 1 ? "" : "s"}`
          : error instanceof Error
            ? error.message
            : "Validation failed",
      );
      setSidebar("validate");
    } finally {
      setValidating(false);
    }
  }

  async function publish(workflowID: string, version: number) {
    await api(`${apiPaths.workflowVersion(orgId, projectId, workflowID, version)}:publish`, {
      method: "POST",
    });
    setDeployedVersion(version);
    setCurrentVersion(version);
    baseline.current = stableStringify(
      serializeGraph(nodesRef.current, edgesRef.current, metaRef.current),
    );
    setDirty(false);
    await invalidateWorkflow(workflowID);
    toast.success(`v${version} deployed`);
  }

  async function invalidateWorkflow(workflowID: string) {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["workflow", orgId, projectId, workflowID] }),
      queryClient.invalidateQueries({
        queryKey: ["workflow-versions", orgId, projectId, workflowID],
      }),
      queryClient.invalidateQueries({ queryKey: ["workflow-catalog", orgId, projectId] }),
      queryClient.invalidateQueries({ queryKey: ["workflows", orgId, projectId] }),
    ]);
  }

  async function persist(deploy: boolean) {
    if (!orgId || !projectId) return;
    if (!workflowId && !name.trim()) {
      toast.error("Name the workflow before saving.");
      return;
    }
    setSaving(true);
    try {
      if (!workflowId) {
        const created = await api<Workflow>(apiPaths.workflows(orgId, projectId), {
          method: "POST",
          json: { name: name.trim(), description, version: { graph } },
        });
        await invalidateWorkflow(created.id);
        if (deploy) {
          try {
            await api(`${apiPaths.workflowVersion(orgId, projectId, created.id, 1)}:publish`, {
              method: "POST",
            });
            toast.success("Workflow created and deployed");
          } catch (error) {
            toast.error(
              error instanceof Error
                ? `Draft saved but deploy failed: ${error.message}`
                : "Draft saved but deploy failed",
            );
          }
        } else {
          toast.success("Draft saved");
        }
        router.replace(`/workflows/${created.id}/edit`);
        return;
      }
      if (!dirty && currentVersion > 0) {
        if (deploy) {
          await publish(workflowId, currentVersion);
        } else {
          toast.success(`v${currentVersion} already saved`);
        }
        return;
      }
      const version = await api<WorkflowVersion>(
        apiPaths.workflowVersions(orgId, projectId, workflowId),
        { method: "POST", json: { graph } },
      );
      setCurrentVersion(version.version);
      baseline.current = stableStringify(graph);
      setDirty(false);
      await invalidateWorkflow(workflowId);
      toast.success(`Draft v${version.version} saved`);
      if (deploy) await publish(workflowId, version.version);
    } catch (error) {
      if (error instanceof ApiError && error.code === "invalid_graph") {
        const problems = serverIssues(error, graph);
        setServerProblems(problems);
        setValid(false);
        setSidebar("validate");
        toast.error(
          `Graph has ${problems.length || "server"} problem${problems.length === 1 ? "" : "s"}`,
        );
      } else {
        toast.error(error instanceof Error ? error.message : "Could not save workflow");
      }
    } finally {
      setSaving(false);
    }
  }

  async function activate(version: number) {
    if (!workflowId) return;
    setActivating(version);
    try {
      await api(`${apiPaths.workflowVersion(orgId, projectId, workflowId, version)}:publish`, {
        method: "POST",
      });
      setDeployedVersion(version);
      await invalidateWorkflow(workflowId);
      toast.success(`v${version} deployed`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not deploy version");
    } finally {
      setActivating(null);
    }
  }

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input,textarea,select,[contenteditable='true']")) return;
      const mod = event.metaKey || event.ctrlKey;
      if (mod && event.key.toLowerCase() === "z") {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
      } else if (mod && event.key.toLowerCase() === "y") {
        event.preventDefault();
        redo();
      } else if (mod && event.key.toLowerCase() === "c") {
        copySelection();
      } else if (mod && event.key.toLowerCase() === "v") {
        event.preventDefault();
        pasteSelection();
      } else if (mod && event.key.toLowerCase() === "s") {
        event.preventDefault();
        void persist(false);
      } else if (mod && event.key === "Enter") {
        event.preventDefault();
        void validateGraph();
      } else if ((event.key === "Delete" || event.key === "Backspace") && !mod) {
        event.preventDefault();
        deleteSelection();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  if (!orgId || !projectId) {
    return (
      <EmptyState
        title="Choose an organization and project"
        description="The builder stores versions in the selected project."
      />
    );
  }
  if (workflowId && workflow.isPending) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-[60vh] w-full" />
      </div>
    );
  }
  if (workflowId && (workflow.isError || !workflow.data)) {
    return (
      <EmptyState
        icon={WorkflowIcon}
        title="Workflow not found"
        description="The workflow may belong to another project or have been removed."
        action={
          <Link href="/workflows" className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ArrowLeft className="size-3.5" /> Workflows
          </Link>
        }
      />
    );
  }
  if (!initialized) {
    return <Skeleton className="h-[70vh] w-full" />;
  }

  const title = workflow.data?.name ?? (name || "Untitled workflow");
  const displayVersion = currentVersion || workflow.data?.latest_version || 0;

  return (
    <>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Link
          href={workflowId ? `/workflows/${workflowId}` : "/workflows"}
          className="inline-flex items-center gap-1 hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" /> {workflowId ? "Workflow" : "Workflows"}
        </Link>
        <span>/</span>
        <span>Builder</span>
      </div>

      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          {workflowId ? (
            <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
          ) : (
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Workflow name"
              className="h-9 max-w-md text-base font-semibold"
              aria-label="Workflow name"
            />
          )}
          {workflowId ? (
            <p className="mt-0.5 text-xs text-muted-foreground">
              {workflow.data?.description || workflow.data?.id}
            </p>
          ) : (
            <Input
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Short description (optional)"
              className="mt-1.5 h-8 max-w-md text-xs"
              aria-label="Workflow description"
            />
          )}
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline" className="font-mono">
              v{displayVersion || "new"}
            </Badge>
            {dirty ? (
              <Badge variant="warning">unsaved</Badge>
            ) : (
              <Badge variant="success">saved</Badge>
            )}
            {deployedVersion ? (
              <Badge variant="success">deployed v{deployedVersion}</Badge>
            ) : (
              <Badge variant="neutral">not deployed</Badge>
            )}
            <span className="text-[11px] text-muted-foreground">
              {nodes.length} nodes · {edges.length} edges
            </span>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            size="sm"
            variant="outline"
            onClick={undo}
            disabled={history.current.past.length === 0}
            title="Undo (⌘Z)"
          >
            <Undo2 className="size-3.5" />
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={redo}
            disabled={history.current.future.length === 0}
            title="Redo (⇧⌘Z)"
          >
            <Redo2 className="size-3.5" />
          </Button>
          <Button size="sm" variant="outline" onClick={autoLayout} title="Auto layout">
            <LayoutGrid className="size-3.5" /> Layout
          </Button>
          <Button size="sm" variant="outline" onClick={() => setFitToken((token) => token + 1)}>
            Fit
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setRunOpen(true)}
            disabled={dirty || !workflowId}
            title={dirty ? "Deploy current changes before running" : "Run workflow"}
          >
            <Play className="size-3.5" /> Run
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void validateGraph()}
            disabled={validating}
          >
            <ShieldCheck className="size-3.5" /> Validate
          </Button>
          <Button size="sm" variant="outline" onClick={() => void persist(false)} disabled={saving}>
            <Save className="size-3.5" /> Save draft
          </Button>
          <Button size="sm" onClick={() => void persist(true)} disabled={saving}>
            <Rocket className="size-3.5" /> Deploy
          </Button>
        </div>
      </div>

      <div className="grid gap-3 xl:grid-cols-[14rem_minmax(0,1fr)_24rem]">
        <aside className="hidden h-[70vh] overflow-hidden rounded-lg border border-border bg-card xl:flex xl:h-[calc(100vh-14rem)]">
          <NodeLibrary
            onAdd={(type) => addNode(type)}
            onDragStart={(event, type) => event.dataTransfer.setData("application/reactflow", type)}
          />
        </aside>

        <div className="h-[70vh] min-h-[480px] xl:h-[calc(100vh-14rem)]">
          <ReactFlowProvider>
            <BuilderCanvas
              nodes={displayNodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={connect}
              onDropNode={addNode}
              onNodeDragStart={() => {
                dragSnapshot.current = currentSnapshot();
              }}
              onNodeDragStop={() => {
                const before = dragSnapshot.current;
                dragSnapshot.current = null;
                if (!before) return;
                const moved = before.nodes.some((node) => {
                  const current = nodesRef.current.find((item) => item.id === node.id);
                  return (
                    current &&
                    (current.position.x !== node.position.x ||
                      current.position.y !== node.position.y)
                  );
                });
                if (moved) pushHistory("drag", before);
              }}
              onSelectionChange={(nodeIds) => {
                if (executionId && nodeIds[0]) setSidebar("execution");
              }}
              focusNodeId={focusNodeId}
              focusToken={focusToken}
              fitToken={fitToken}
            />
          </ReactFlowProvider>
        </div>

        <aside className="h-[70vh] overflow-hidden rounded-lg border border-border bg-card xl:h-[calc(100vh-14rem)]">
          <Tabs
            value={sidebar}
            onValueChange={setSidebar}
            items={[
              { value: "configure", label: "Config" },
              { value: "validate", label: "Validate", count: issues.length },
              { value: "versions", label: "Versions", count: versions.length },
              { value: "execution", label: "Exec" },
              { value: "settings", label: "Graph" },
            ]}
            className="px-2"
          />
          <div className="scrollbar-thin h-[calc(100%-2.5rem)] overflow-y-auto">
            <TabPanel>
              {sidebar === "configure" ? (
                <NodeInspector
                  orgId={orgId}
                  projectId={projectId}
                  node={selectedNode}
                  nodes={graph.nodes}
                  edges={graph.edges}
                  upstreamIds={upstreamIds}
                  onChange={updateNode}
                  onRename={renameNode}
                  onAddEdge={addEdgeManually}
                  onPatchEdge={patchEdge}
                  onRemoveEdge={removeEdge}
                  onDelete={() => selectedNode && removeNodes([selectedNode.id])}
                />
              ) : null}
              {sidebar === "validate" ? (
                <ValidationPanel
                  issues={issues}
                  valid={valid}
                  validating={validating}
                  onValidate={() => void validateGraph()}
                  onFocus={focusOnNode}
                />
              ) : null}
              {sidebar === "versions" ? (
                <VersionPanel
                  orgId={orgId}
                  projectId={projectId}
                  workflowId={workflowId ?? ""}
                  versions={versions}
                  currentVersion={currentVersion}
                  deployedVersion={deployedVersion}
                  onActivate={(version) => void activate(version)}
                  activating={activating}
                />
              ) : null}
              {sidebar === "execution" ? (
                executionId ? (
                  <ExecutionPanel
                    orgId={orgId}
                    projectId={projectId}
                    executionId={executionId}
                    selectedNodeId={selectedNode?.id}
                    onClear={() => setExecutionId("")}
                  />
                ) : (
                  <div className="p-4">
                    <p className="text-sm font-medium">No execution yet</p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      Deploy a version, then run it to see node state, attempts, errors and logs
                      here.
                    </p>
                    <Button
                      size="sm"
                      className="mt-3"
                      onClick={() => setRunOpen(true)}
                      disabled={dirty || !workflowId}
                    >
                      <Play className="size-3.5" /> Run workflow
                    </Button>
                  </div>
                )
              ) : null}
              {sidebar === "settings" ? (
                <GraphSettingsPanel key={currentVersion} meta={meta} onChange={setMeta} />
              ) : null}
            </TabPanel>
          </div>
        </aside>

        <div className="xl:hidden">
          <div className="h-72 overflow-hidden rounded-lg border border-border bg-card">
            <NodeLibrary
              onAdd={(type) => addNode(type)}
              onDragStart={(event, type) =>
                event.dataTransfer.setData("application/reactflow", type)
              }
            />
          </div>
        </div>
      </div>

      <RunDialog
        open={runOpen}
        onOpenChange={setRunOpen}
        orgId={orgId}
        projectId={projectId}
        workflowId={workflowId ?? ""}
        graph={graph}
        versions={versions}
        defaultVersion={deployedVersion}
        onStarted={(id) => {
          setExecutionId(id);
          setSidebar("execution");
        }}
      />
    </>
  );
}

function GraphSettingsPanel({
  meta,
  onChange,
}: {
  meta: GraphMeta;
  onChange: (meta: GraphMeta) => void;
}) {
  const [inputSchema, setInputSchema] = useState(() =>
    meta.input_schema ? JSON.stringify(meta.input_schema, null, 2) : "",
  );
  const [policy, setPolicy] = useState(() =>
    meta.policy ? JSON.stringify(meta.policy, null, 2) : "",
  );
  const [errors, setErrors] = useState<{ input?: string; policy?: string }>({});

  return (
    <div className="space-y-5 p-4">
      <div>
        <p className="text-sm font-medium">Graph settings</p>
        <p className="text-[11px] text-muted-foreground">
          These values are part of the immutable workflow version.
        </p>
      </div>
      <JsonArea
        label="Input schema"
        value={inputSchema}
        onChange={setInputSchema}
        error={errors.input}
        onBlur={() => {
          if (!inputSchema.trim()) {
            onChange({ ...meta, input_schema: undefined });
            setErrors((current) => ({ ...current, input: undefined }));
            return;
          }
          try {
            const parsed = JSON.parse(inputSchema);
            onChange({ ...meta, input_schema: parsed });
            setErrors((current) => ({ ...current, input: undefined }));
          } catch {
            setErrors((current) => ({ ...current, input: "Invalid JSON" }));
          }
        }}
      />
      <JsonArea
        label="Workflow policy"
        value={policy}
        onChange={setPolicy}
        error={errors.policy}
        onBlur={() => {
          if (!policy.trim()) {
            onChange({ ...meta, policy: undefined });
            setErrors((current) => ({ ...current, policy: undefined }));
            return;
          }
          try {
            const parsed = JSON.parse(policy);
            onChange({ ...meta, policy: parsed });
            setErrors((current) => ({ ...current, policy: undefined }));
          } catch {
            setErrors((current) => ({ ...current, policy: "Invalid JSON" }));
          }
        }}
      />
      <p className="text-[11px] text-muted-foreground">
        Example policy:{" "}
        <span className="font-mono">{'{"on_failure":"fail_fast","retry":{"max_attempts":2}}'}</span>
      </p>
    </div>
  );
}

function JsonArea({
  label,
  value,
  onChange,
  onBlur,
  error,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  onBlur: () => void;
  error?: string;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium">{label}</span>
      <textarea
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
        rows={6}
        spellCheck={false}
        placeholder="{}"
        className={cn(
          "w-full rounded-md border bg-background p-2 font-mono text-[11px]",
          error ? "border-danger" : "border-input",
        )}
      />
      {error ? <span className="block text-[11px] text-danger">{error}</span> : null}
    </label>
  );
}
