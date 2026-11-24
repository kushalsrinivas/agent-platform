import type { Edge, Node } from "@xyflow/react";

import type { WorkflowEdge, WorkflowGraph, WorkflowNode } from "@/lib/api/types";

export type BuilderNodeData = {
  node: WorkflowNode;
  executionState?: string;
  durationMs?: number | null;
  hasError?: boolean;
};

export type BuilderNode = Node<BuilderNodeData & Record<string, unknown>, "builder">;
export type BuilderEdgeData = {
  when?: string;
  dataMapping?: Record<string, string>;
  priority?: number;
};
export type BuilderEdge = Edge<BuilderEdgeData>;

export type ValidationIssue = {
  id: string;
  message: string;
  nodeId?: string;
  source: "local" | "server";
};

const NODE_ID = /^[a-z][a-z0-9_]{0,63}$/;
const NODE_TYPES = new Set([
  "start",
  "end",
  "agent",
  "llm",
  "tool",
  "condition",
  "transform",
  "parallel",
  "join",
  "delay",
  "approval",
  "subworkflow",
  "loop",
]);

export function defaultNode(type: string): WorkflowNode {
  switch (type) {
    case "agent":
    case "llm":
      return {
        id: "",
        type,
        worker_pool: "default",
        timeout_seconds: 300,
        retry: { max_attempts: 1 },
      };
    case "tool":
      return { id: "", type, timeout_seconds: 120, retry: { max_attempts: 1 } };
    case "condition":
      return { id: "", type, config: { expression: "" } };
    case "transform":
      return { id: "", type, config: { mapping: {} } };
    case "join":
      return { id: "", type, config: { join: { mode: "all", on_partial: "fail" } } };
    case "delay":
      return { id: "", type, config: { delay: { seconds: 60 } } };
    case "approval":
      return {
        id: "",
        type,
        config: { approval: { prompt: "Approve this step?", policy: "any" } },
      };
    case "subworkflow":
      return { id: "", type, config: { workflow: { workflow_id: "", version: 1 } } };
    case "loop":
      return {
        id: "",
        type,
        config: {
          workflow: { workflow_id: "", version: 1 },
          loop: { max_iterations: 3 },
        },
      };
    default:
      return { id: "", type };
  }
}

export function nextNodeId(type: string, nodes: { id: string }[]): string {
  const base = type.replace(/[^a-z0-9_]/g, "_").slice(0, 52) || "node";
  for (let index = 1; index < 10_000; index += 1) {
    const candidate = `${base}_${index}`;
    if (!nodes.some((node) => node.id === candidate)) return candidate;
  }
  return `node_${Date.now()}`;
}

export function layoutForGraph(graph: WorkflowGraph): Map<string, { x: number; y: number }> {
  const nodes = graph.nodes ?? [];
  const edges = graph.edges ?? [];
  const incoming = new Map<string, string[]>();
  for (const node of nodes) incoming.set(node.id, []);
  for (const edge of edges) incoming.get(edge.to)?.push(edge.from);

  const levels = new Map<string, number>();
  const queue = nodes
    .filter((node) => (incoming.get(node.id)?.length ?? 0) === 0)
    .map((node) => node.id);
  for (const id of queue) levels.set(id, 0);
  let cursor = 0;
  while (cursor < queue.length) {
    const id = queue[cursor];
    cursor += 1;
    if (id === undefined) break;
    const level = levels.get(id) ?? 0;
    for (const edge of edges) {
      if (edge.from !== id) continue;
      const next = Math.max(levels.get(edge.to) ?? 0, level + 1);
      levels.set(edge.to, next);
      if (!queue.includes(edge.to)) queue.push(edge.to);
    }
  }

  const byLevel = new Map<number, string[]>();
  nodes.forEach((node, index) => {
    const level = levels.get(node.id) ?? index;
    byLevel.set(level, [...(byLevel.get(level) ?? []), node.id]);
  });

  const positions = new Map<string, { x: number; y: number }>();
  for (const [level, ids] of byLevel) {
    ids.forEach((id, row) => {
      positions.set(id, {
        x: level * 300,
        y: (row - (ids.length - 1) / 2) * 150,
      });
    });
  }
  return positions;
}

export function toCanvas(graph: WorkflowGraph | undefined): {
  nodes: BuilderNode[];
  edges: BuilderEdge[];
} {
  if (!graph) return { nodes: [], edges: [] };
  const positions = layoutForGraph(graph);
  const nodes: BuilderNode[] = graph.nodes.map((node) => ({
    id: node.id,
    type: "builder",
    position: positions.get(node.id) ?? { x: 0, y: 0 },
    data: { node: structuredClone(node), hasError: false },
  }));
  const edges: BuilderEdge[] = graph.edges.map((edge) => ({
    id: edgeId(edge.from, edge.to),
    source: edge.from,
    target: edge.to,
    type: "smoothstep",
    label: edge.when || undefined,
    data: {
      when: edge.when,
      dataMapping: edge.data_mapping,
      priority: edge.priority,
    },
    markerEnd: { type: "arrowclosed" as never },
  }));
  return { nodes, edges };
}

export function serializeGraph(
  nodes: BuilderNode[],
  edges: BuilderEdge[],
  meta?: Pick<WorkflowGraph, "policy" | "input_schema">,
): WorkflowGraph {
  const graphEdges: WorkflowEdge[] = edges.map((edge) => {
    const result: WorkflowEdge = { from: edge.source, to: edge.target };
    const when = edge.data?.when?.trim();
    if (when) result.when = when;
    if (edge.data?.dataMapping && Object.keys(edge.data.dataMapping).length > 0) {
      result.data_mapping = edge.data.dataMapping;
    }
    if (typeof edge.data?.priority === "number") result.priority = edge.data.priority;
    return result;
  });
  const graph: WorkflowGraph = {
    nodes: nodes.map((item) => JSON.parse(JSON.stringify(item.data.node)) as WorkflowNode),
    edges: graphEdges,
  };
  if (meta?.policy) graph.policy = meta.policy;
  if (meta?.input_schema) graph.input_schema = meta.input_schema;
  return graph;
}

export function edgeId(from: string, to: string): string {
  return `edge:${from}:${to}`;
}

export function applyLayout(nodes: BuilderNode[], edges: BuilderEdge[]): BuilderNode[] {
  const positions = layoutForGraph(serializeGraph(nodes, edges));
  return nodes.map((node) => ({ ...node, position: positions.get(node.id) ?? node.position }));
}

export function nodeSummary(node: WorkflowNode): string {
  if (node.agent) {
    return node.agent.agent_id
      ? `${node.agent.agent_id} v${node.agent.version}`
      : "agent not selected";
  }
  if (node.tool)
    return node.tool.tool_id ? `${node.tool.tool_id} v${node.tool.version}` : "tool not selected";
  switch (node.type) {
    case "condition":
      return node.config?.expression || "expression not set";
    case "transform":
      return `${Object.keys(node.config?.mapping ?? {}).length} mapping(s)`;
    case "join":
      return `wait ${node.config?.join?.mode ?? "all"}`;
    case "delay":
      return node.config?.delay?.seconds
        ? `${node.config.delay.seconds}s`
        : node.config?.delay?.event || node.config?.delay?.until || "wait not set";
    case "approval":
      return node.config?.approval?.prompt || "human decision";
    case "subworkflow":
    case "loop":
      return node.config?.workflow?.workflow_id || "workflow not selected";
    case "start":
      return "entry";
    case "end":
      return "termination";
    case "parallel":
      return "fan-out marker";
    default:
      return node.type;
  }
}

export function localValidate(graph: WorkflowGraph): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const add = (message: string, nodeId?: string) =>
    issues.push({
      id: `local:${issues.length}:${nodeId ?? ""}:${message}`,
      message,
      nodeId,
      source: "local",
    });
  const nodes = graph.nodes ?? [];
  const edges = graph.edges ?? [];

  if (nodes.length === 0) add("Add at least one node.");
  if (nodes.length > 500) add("A graph may contain at most 500 nodes.");
  if (edges.length > 2000) add("A graph may contain at most 2000 edges.");

  const byId = new Map<string, WorkflowNode>();
  const incoming = new Map<string, string[]>();
  const outgoing = new Map<string, string[]>();
  for (const node of nodes) {
    incoming.set(node.id, []);
    outgoing.set(node.id, []);
  }

  nodes.forEach((node, index) => {
    if (!NODE_ID.test(node.id))
      add(`Node id "${node.id}" must match ^[a-z][a-z0-9_]{0,63}$.`, node.id);
    if (byId.has(node.id)) add(`Duplicate node id "${node.id}".`, node.id);
    if (byId.has(node.id)) return;
    byId.set(node.id, node);
    const label = node.id || `nodes[${index}]`;
    if (!NODE_TYPES.has(node.type)) add(`${label}: type "${node.type}" is not supported.`, node.id);
    if ((node.type === "agent" || node.type === "llm") && !node.agent) {
      add(`${label}: select an agent and version.`, node.id);
    }
    if ((node.type === "agent" || node.type === "llm") && node.tool) {
      add(`${label}: agent nodes cannot carry a tool reference.`, node.id);
    }
    if (node.type === "tool" && !node.tool) add(`${label}: select a tool and version.`, node.id);
    if (node.type === "tool" && node.agent)
      add(`${label}: tool nodes cannot carry an agent reference.`, node.id);
    if (node.agent && (!node.agent.agent_id || node.agent.version < 1)) {
      add(`${label}: agent reference and version are required.`, node.id);
    }
    if (node.tool && (!node.tool.tool_id || node.tool.version < 1)) {
      add(`${label}: tool reference and version are required.`, node.id);
    }
    if (!["agent", "llm", "tool"].includes(node.type) && (node.agent || node.tool)) {
      add(`${label}: agent/tool references are only valid on worker nodes.`, node.id);
    }
    if (node.worker_pool && !["agent", "llm", "tool"].includes(node.type)) {
      add(`${label}: worker_pool is only valid on worker nodes.`, node.id);
    }
    if (
      node.timeout_seconds !== undefined &&
      (node.timeout_seconds < 1 || node.timeout_seconds > 86_400)
    ) {
      add(`${label}: timeout must be 1..86400 seconds.`, node.id);
    }
    if (
      node.retry?.max_attempts !== undefined &&
      (node.retry.max_attempts < 1 || node.retry.max_attempts > 10)
    ) {
      add(`${label}: retry max_attempts must be 1..10.`, node.id);
    }
    if (node.retry?.backoff_base_ms !== undefined && node.retry.backoff_base_ms < 100) {
      add(`${label}: backoff base must be at least 100ms.`, node.id);
    }
    if (
      node.retry?.backoff_base_ms !== undefined &&
      node.retry?.backoff_max_ms !== undefined &&
      node.retry.backoff_base_ms > node.retry.backoff_max_ms
    ) {
      add(`${label}: backoff base cannot exceed backoff max.`, node.id);
    }
    if (
      node.side_effect_class === "non_idempotent" &&
      (node.retry?.max_attempts ?? 1) > 1 &&
      !node.idempotency_key
    ) {
      add(`${label}: non-idempotent nodes need an idempotency key to retry.`, node.id);
    }

    const config = node.config;
    if (node.type === "condition") {
      if (!config?.expression?.trim()) add(`${label}: condition expression is required.`, node.id);
    }
    if (node.type === "transform") {
      if (!config?.mapping || Object.keys(config.mapping).length === 0) {
        add(`${label}: add at least one output mapping.`, node.id);
      }
      for (const [target, expression] of Object.entries(config?.mapping ?? {})) {
        if (!target.trim()) add(`${label}: mapping targets cannot be empty.`, node.id);
        if (!expression.trim()) add(`${label}: mapping "${target}" needs an expression.`, node.id);
      }
    }
    if (node.type === "join") {
      if (!config?.join?.mode) add(`${label}: join mode is required.`, node.id);
      if (config?.join?.mode === "successful" && !config.join.successful) {
        add(`${label}: successful joins need a success count.`, node.id);
      }
    }
    if (node.type === "delay") {
      const values = [config?.delay?.seconds, config?.delay?.until, config?.delay?.event].filter(
        (value) => value !== undefined && value !== "",
      );
      if (values.length !== 1)
        add(`${label}: set exactly one of seconds, until or event.`, node.id);
      if (
        config?.delay?.seconds !== undefined &&
        (config.delay.seconds < 1 || config.delay.seconds > 604_800)
      ) {
        add(`${label}: delay seconds must be 1..604800.`, node.id);
      }
    }
    if (node.type === "approval") {
      if (config?.approval?.policy === "all" && (config.approval.approvers ?? []).length === 0) {
        add(`${label}: policy "all" requires at least one approver.`, node.id);
      }
    }
    if ((node.type === "subworkflow" || node.type === "loop") && !config?.workflow?.workflow_id) {
      add(`${label}: select a child workflow and version.`, node.id);
    }
    if (
      (node.type === "subworkflow" || node.type === "loop") &&
      (config?.workflow?.version ?? 0) < 1
    ) {
      add(`${label}: child workflow version must be at least 1.`, node.id);
    }
    if (node.type === "loop" && !config?.loop) {
      add(`${label}: loop configuration is required.`, node.id);
    }
    if (
      node.type === "loop" &&
      config?.loop &&
      (config.loop.max_iterations < 1 || config.loop.max_iterations > 100)
    ) {
      add(`${label}: max iterations must be 1..100.`, node.id);
    }
    if (node.type === "loop" && config?.loop?.max_iterations === 100) {
      issues.push({
        id: `local:${issues.length}:${node.id}:bounded`,
        message: `${label}: 100 iterations is the hard cap; check this loop is intentional.`,
        nodeId: node.id,
        source: "local",
      });
    }
  });

  const seen = new Set<string>();
  const edgeList: WorkflowEdge[] = [];
  edges.forEach((edge, index) => {
    const label = `edges[${index}]`;
    const key = `${edge.from}\u0000${edge.to}`;
    if (edge.from === edge.to) add(`${label}: self edges are not allowed.`, edge.from);
    if (!byId.has(edge.from)) add(`${label}: source "${edge.from}" does not exist.`, edge.from);
    if (!byId.has(edge.to)) add(`${label}: target "${edge.to}" does not exist.`, edge.to);
    if (seen.has(key)) add(`${label}: duplicate edge ${edge.from} -> ${edge.to}.`, edge.from);
    seen.add(key);
    if (edge.when !== undefined && !edge.when.trim())
      add(`${label}: edge condition is empty.`, edge.from);
    if (edge.priority !== undefined && (edge.priority < -100 || edge.priority > 100)) {
      add(`${label}: priority must be -100..100.`, edge.from);
    }
    incoming.get(edge.to)?.push(edge.from);
    outgoing.get(edge.from)?.push(edge.to);
    edgeList.push(edge);
  });

  if (!nodes.some((node) => (incoming.get(node.id)?.length ?? 0) === 0)) {
    add("Workflow has no entry point.");
  }
  const starts = nodes.filter((node) => node.type === "start");
  const ends = nodes.filter((node) => node.type === "end");
  if (starts.length > 1) add("Only one start node is allowed.");
  if (ends.length > 1) add("Only one end node is allowed.");
  for (const node of starts) {
    if ((incoming.get(node.id)?.length ?? 0) > 0)
      add(`Start node "${node.id}" cannot have incoming edges.`, node.id);
  }
  for (const node of ends) {
    if ((outgoing.get(node.id)?.length ?? 0) > 0)
      add(`End node "${node.id}" cannot have outgoing edges.`, node.id);
  }

  const reachable = new Set<string>();
  if (starts[0]) {
    const queue = [starts[0].id];
    while (queue.length > 0) {
      const id = queue.shift();
      if (id === undefined || reachable.has(id)) continue;
      reachable.add(id);
      queue.push(...(outgoing.get(id) ?? []));
    }
    for (const node of nodes) {
      if (!reachable.has(node.id)) add(`Node "${node.id}" is not reachable from start.`, node.id);
    }
  }

  if (ends[0]) {
    const reachesEnd = new Set<string>();
    const queue = [ends[0].id];
    while (queue.length > 0) {
      const id = queue.shift();
      if (id === undefined || reachesEnd.has(id)) continue;
      reachesEnd.add(id);
      queue.push(...(incoming.get(id) ?? []));
    }
    for (const node of nodes) {
      if (!reachesEnd.has(node.id)) add(`Node "${node.id}" cannot reach end.`, node.id);
    }
  }

  const ancestors = (target: string): Set<string> => {
    const result = new Set<string>();
    const queue = [...(incoming.get(target) ?? [])];
    while (queue.length > 0) {
      const id = queue.shift();
      if (id === undefined || result.has(id)) continue;
      result.add(id);
      queue.push(...(incoming.get(id) ?? []));
    }
    return result;
  };
  for (const node of nodes) {
    if (!node.input_from) continue;
    const upstream = ancestors(node.id);
    if (node.input_from.length > 16)
      add(`Node "${node.id}" may select at most 16 inputs.`, node.id);
    for (const ref of node.input_from) {
      if (!byId.has(ref)) add(`Node "${node.id}" reads unknown node "${ref}".`, node.id);
      else if (!upstream.has(ref))
        add(`Node "${node.id}" reads non-upstream node "${ref}".`, node.id);
      else if (ref === node.id) add(`Node "${node.id}" cannot read its own output.`, node.id);
    }
  }

  const visiting = new Set<string>();
  const done = new Set<string>();
  const cycle = (id: string): boolean => {
    if (visiting.has(id)) return true;
    if (done.has(id)) return false;
    visiting.add(id);
    for (const next of outgoing.get(id) ?? []) {
      if (cycle(next)) return true;
    }
    visiting.delete(id);
    done.add(id);
    return false;
  };
  if (nodes.some((node) => cycle(node.id))) {
    add("Workflow contains a cycle. Use a bounded loop node instead.");
  }

  return dedupeIssues(issues);
}

export function locateProblem(problem: string, graph: WorkflowGraph): string | undefined {
  const nodeIndex = problem.match(/nodes\[(\d+)\]/);
  if (nodeIndex) {
    const index = Number(nodeIndex[1]);
    if (graph.nodes[index]) return graph.nodes[index].id;
  }
  const edgeIndex = problem.match(/edges\[(\d+)\]/);
  if (edgeIndex) {
    const index = Number(edgeIndex[1]);
    if (graph.edges[index]) return graph.edges[index].from;
  }
  for (const match of problem.matchAll(/node "([a-z][a-z0-9_]{0,63})"/g)) {
    if (graph.nodes.some((node) => node.id === match[1])) return match[1];
  }
  for (const node of graph.nodes) {
    if (problem.includes(`"${node.id}"`)) return node.id;
  }
  return undefined;
}

export function serverIssues(error: unknown, graph: WorkflowGraph): ValidationIssue[] {
  const details = (error as { details?: unknown } | undefined)?.details;
  if (!details || typeof details !== "object") return [];
  const raw = (details as { problems?: unknown }).problems;
  const problems = Array.isArray(raw)
    ? raw.map(String)
    : typeof raw === "string"
      ? raw
          .replace(/^contract violation:\s*/i, "")
          .split("\n")
          .map((line) => line.trim())
          .filter(Boolean)
      : [];
  return problems.map((message, index) => ({
    id: `server:${index}:${message}`,
    message,
    nodeId: locateProblem(message, graph),
    source: "server" as const,
  }));
}

export function dedupeIssues(issues: ValidationIssue[]): ValidationIssue[] {
  const seen = new Set<string>();
  return issues.filter((issue) => {
    const key = `${issue.nodeId ?? ""}:${issue.message}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function stableStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, item]) => item !== undefined)
      .sort(([left], [right]) => left.localeCompare(right));
    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

export type GraphDiff = {
  addedNodes: string[];
  removedNodes: string[];
  changedNodes: string[];
  addedEdges: string[];
  removedEdges: string[];
  changedEdges: string[];
  policyChanged: boolean;
  inputSchemaChanged: boolean;
};

export function compareGraphs(base: WorkflowGraph, target: WorkflowGraph): GraphDiff {
  const baseNodes = new Map(base.nodes.map((node) => [node.id, stableStringify(node)]));
  const targetNodes = new Map(target.nodes.map((node) => [node.id, stableStringify(node)]));
  const node = (id: string) =>
    graphNodeLabel(
      target.nodes.find((item) => item.id === id) ?? base.nodes.find((item) => item.id === id),
    );
  const edgeKey = (edge: WorkflowEdge) => `${edge.from} -> ${edge.to}`;
  const baseEdges = new Map(base.edges.map((edge) => [edgeKey(edge), stableStringify(edge)]));
  const targetEdges = new Map(target.edges.map((edge) => [edgeKey(edge), stableStringify(edge)]));

  return {
    addedNodes: [...targetNodes.keys()].filter((id) => !baseNodes.has(id)).map(node),
    removedNodes: [...baseNodes.keys()].filter((id) => !targetNodes.has(id)).map(node),
    changedNodes: [...targetNodes.keys()]
      .filter((id) => baseNodes.has(id) && baseNodes.get(id) !== targetNodes.get(id))
      .map(node),
    addedEdges: [...targetEdges.keys()].filter((key) => !baseEdges.has(key)),
    removedEdges: [...baseEdges.keys()].filter((key) => !targetEdges.has(key)),
    changedEdges: [...targetEdges.keys()].filter(
      (key) => baseEdges.has(key) && baseEdges.get(key) !== targetEdges.get(key),
    ),
    policyChanged: stableStringify(base.policy) !== stableStringify(target.policy),
    inputSchemaChanged: stableStringify(base.input_schema) !== stableStringify(target.input_schema),
  };
}

function graphNodeLabel(node: WorkflowNode | undefined): string {
  return node ? `${node.id} (${node.type})` : "unknown";
}
