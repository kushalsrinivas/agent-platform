import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { api, apiPaths, isUnauthorized } from "@/lib/api";
import type {
  Agent,
  AgentVersion,
  Approval,
  Environment,
  Event,
  Execution,
  MCPServer,
  Meta,
  Organization,
  Page,
  Project,
  Task,
  TaskDetail,
  Tool,
  Usage,
  Worker,
  Workflow,
  WorkflowVersion,
} from "@/lib/api/types";
import { useWorkspace } from "@/stores/workspace";

export const queryKeys = {
  organizations: ["organizations"] as const,
  projects: (orgId: string) => ["projects", orgId] as const,
  environments: (orgId: string, projectId: string) => ["environments", orgId, projectId] as const,
  agents: (orgId: string, projectId: string) => ["agents", orgId, projectId] as const,
  agentVersions: (orgId: string, projectId: string, agentId: string) =>
    ["agent-versions", orgId, projectId, agentId] as const,
  workflows: (orgId: string, projectId: string) => ["workflows", orgId, projectId] as const,
  workflowVersions: (orgId: string, projectId: string, workflowId: string) =>
    ["workflow-versions", orgId, projectId, workflowId] as const,
  workflowVersion: (orgId: string, projectId: string, workflowId: string, version: number) =>
    ["workflow-version", orgId, projectId, workflowId, version] as const,
  executions: (orgId: string, projectId: string, params: string) =>
    ["executions", orgId, projectId, params] as const,
  execution: (orgId: string, projectId: string, executionId: string) =>
    ["execution", orgId, projectId, executionId] as const,
  executionInput: (orgId: string, projectId: string, executionId: string) =>
    ["execution-input", orgId, projectId, executionId] as const,
  tasks: (orgId: string, projectId: string, executionId: string) =>
    ["tasks", orgId, projectId, executionId] as const,
  task: (orgId: string, projectId: string, executionId: string, taskId: string) =>
    ["task", orgId, projectId, executionId, taskId] as const,
  events: (orgId: string, projectId: string, executionId: string) =>
    ["events", orgId, projectId, executionId] as const,
  outputs: (orgId: string, projectId: string, executionId: string) =>
    ["execution-outputs", orgId, projectId, executionId] as const,
  executionApprovals: (orgId: string, projectId: string, executionId: string) =>
    ["execution-approvals", orgId, projectId, executionId] as const,
  approvals: (orgId: string, projectId: string, status: string) =>
    ["approvals", orgId, projectId, status] as const,
  usage: (orgId: string, projectId: string, from: string, to: string) =>
    ["usage", orgId, projectId, from, to] as const,
  workers: (orgId: string) => ["workers", orgId] as const,
  tools: (orgId: string, projectId: string) => ["tools", orgId, projectId] as const,
  mcp: (orgId: string, projectId: string) => ["mcp", orgId, projectId] as const,
  meta: ["meta"] as const,
};

export function useWorkspaceScope() {
  const orgId = useWorkspace((state) => state.orgId);
  const projectId = useWorkspace((state) => state.projectId);
  return { orgId, projectId };
}

export function useMeta() {
  return useQuery({ queryKey: queryKeys.meta, queryFn: () => api<Meta>(apiPaths.meta()) });
}

export function useOrganizations() {
  return useQuery({
    queryKey: queryKeys.organizations,
    queryFn: () => api<Page<Organization>>(apiPaths.organizations()),
  });
}

export function useProjects(orgId: string) {
  return useQuery({
    queryKey: queryKeys.projects(orgId),
    queryFn: () => api<Page<Project>>(apiPaths.projects(orgId)),
    enabled: Boolean(orgId),
  });
}

export function useEnvironments(orgId: string, projectId: string) {
  return useQuery({
    queryKey: queryKeys.environments(orgId, projectId),
    queryFn: () => api<Page<Environment>>(apiPaths.environments(orgId, projectId)),
    enabled: Boolean(orgId && projectId),
  });
}

export function useAgents(orgId: string, projectId: string) {
  return useQuery({
    queryKey: queryKeys.agents(orgId, projectId),
    queryFn: () => api<Page<Agent>>(apiPaths.agents(orgId, projectId)),
    enabled: Boolean(orgId && projectId),
  });
}

export function useAgent(orgId: string, projectId: string, agentId: string) {
  return useQuery({
    queryKey: ["agent", orgId, projectId, agentId],
    queryFn: () =>
      api<Agent>(
        `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/agents/${encodeURIComponent(agentId)}`,
      ),
    enabled: Boolean(orgId && projectId && agentId),
  });
}

export function useAgentVersion(
  orgId: string,
  projectId: string,
  agentId: string,
  version: number,
) {
  return useQuery({
    queryKey: ["agent-version", orgId, projectId, agentId, version],
    queryFn: () => api<AgentVersion>(apiPaths.agentVersion(orgId, projectId, agentId, version)),
    enabled: Boolean(orgId && projectId && agentId && version > 0),
  });
}

export function useAgentVersions(orgId: string, projectId: string, agentId: string) {
  return useQuery({
    queryKey: queryKeys.agentVersions(orgId, projectId, agentId),
    queryFn: () => api<Page<AgentVersion>>(apiPaths.agentVersions(orgId, projectId, agentId)),
    enabled: Boolean(orgId && projectId && agentId),
  });
}

export function useWorkflows(orgId: string, projectId: string) {
  return useQuery({
    queryKey: queryKeys.workflows(orgId, projectId),
    queryFn: () => api<Page<Workflow>>(apiPaths.workflows(orgId, projectId)),
    enabled: Boolean(orgId && projectId),
  });
}

export function useWorkflow(orgId: string, projectId: string, workflowId: string) {
  return useQuery({
    queryKey: ["workflow", orgId, projectId, workflowId],
    queryFn: () =>
      api<Workflow>(
        `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/workflows/${encodeURIComponent(workflowId)}`,
      ),
    enabled: Boolean(orgId && projectId && workflowId),
  });
}

export function useWorkflowVersions(orgId: string, projectId: string, workflowId: string) {
  return useQuery({
    queryKey: queryKeys.workflowVersions(orgId, projectId, workflowId),
    queryFn: () =>
      api<Page<WorkflowVersion>>(apiPaths.workflowVersions(orgId, projectId, workflowId)),
    enabled: Boolean(orgId && projectId && workflowId),
  });
}

export function useWorkflowVersion(
  orgId: string,
  projectId: string,
  workflowId: string,
  version: number,
) {
  return useQuery({
    queryKey: queryKeys.workflowVersion(orgId, projectId, workflowId, version),
    queryFn: () =>
      api<WorkflowVersion>(apiPaths.workflowVersion(orgId, projectId, workflowId, version)),
    enabled: Boolean(orgId && projectId && workflowId && version > 0),
  });
}

export type ExecutionFilters = {
  state?: string;
  workflowId?: string;
  createdAfter?: string;
  createdBefore?: string;
  limit?: number;
};

export function executionQuery(filters: ExecutionFilters): string {
  const params = new URLSearchParams();
  if (filters.state) params.set("state", filters.state);
  if (filters.workflowId) params.set("workflow_id", filters.workflowId);
  if (filters.createdAfter) params.set("created_after", filters.createdAfter);
  if (filters.createdBefore) params.set("created_before", filters.createdBefore);
  params.set("limit", String(filters.limit ?? 100));
  params.set("sort", "created_at_desc");
  return params.toString();
}

export function useExecutions(orgId: string, projectId: string, filters: ExecutionFilters = {}) {
  const query = executionQuery(filters);
  return useQuery({
    queryKey: queryKeys.executions(orgId, projectId, query),
    queryFn: () => api<Page<Execution>>(`${apiPaths.executions(orgId, projectId)}?${query}`),
    enabled: Boolean(orgId && projectId),
  });
}

export function useExecution(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.execution(orgId, projectId, executionId),
    queryFn: () => api<Execution>(apiPaths.execution(orgId, projectId, executionId)),
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useExecutionInput(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.executionInput(orgId, projectId, executionId),
    queryFn: () => api<{ input: unknown }>(apiPaths.executionInput(orgId, projectId, executionId)),
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useExecutionTasks(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.tasks(orgId, projectId, executionId),
    queryFn: () => api<Page<Task>>(apiPaths.tasks(orgId, projectId, executionId)),
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useTaskDetail(
  orgId: string,
  projectId: string,
  executionId: string,
  taskId: string,
) {
  return useQuery({
    queryKey: queryKeys.task(orgId, projectId, executionId, taskId),
    queryFn: () => api<TaskDetail>(apiPaths.task(orgId, projectId, executionId, taskId)),
    enabled: Boolean(orgId && projectId && executionId && taskId),
  });
}

export function useExecutionOutputs(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.outputs(orgId, projectId, executionId),
    queryFn: async () => {
      const replay = await api<{ outputs: Record<string, unknown> }>(
        apiPaths.replay(orgId, projectId, executionId),
      );
      return replay.outputs ?? {};
    },
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useExecutionEvents(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.events(orgId, projectId, executionId),
    queryFn: async () => {
      const page = await api<Page<Event>>(
        `${apiPaths.events(orgId, projectId, executionId)}?after=0&limit=500`,
      );
      return page.items;
    },
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useExecutionApprovals(orgId: string, projectId: string, executionId: string) {
  return useQuery({
    queryKey: queryKeys.executionApprovals(orgId, projectId, executionId),
    queryFn: () =>
      api<Page<Approval>>(
        `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/approvals`,
      ),
    enabled: Boolean(orgId && projectId && executionId),
  });
}

export function useApprovals(orgId: string, projectId: string, status = "") {
  const params = new URLSearchParams();
  if (projectId) params.set("project_id", projectId);
  if (status) params.set("status", status);
  return useQuery({
    queryKey: queryKeys.approvals(orgId, projectId, status),
    queryFn: () => api<Page<Approval>>(apiPaths.approvals(orgId, params.toString())),
    enabled: Boolean(orgId),
  });
}

export function useUsage(orgId: string, projectId: string, from: string, to: string) {
  const params = new URLSearchParams();
  if (projectId) params.set("project_id", projectId);
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  return useQuery({
    queryKey: queryKeys.usage(orgId, projectId, from, to),
    queryFn: () => api<Usage>(apiPaths.usage(orgId, params.toString())),
    enabled: Boolean(orgId),
  });
}

export function useWorkers(orgId: string) {
  return useQuery({
    queryKey: queryKeys.workers(orgId),
    queryFn: () => api<Page<Worker>>(apiPaths.workers(orgId)),
    enabled: Boolean(orgId),
    refetchInterval: 10_000,
  });
}

export function useTools(orgId: string, projectId: string) {
  return useQuery({
    queryKey: queryKeys.tools(orgId, projectId),
    queryFn: () => api<Page<Tool>>(apiPaths.tools(orgId, projectId)),
    enabled: Boolean(orgId && projectId),
  });
}

export function useMCPServers(orgId: string, projectId: string) {
  return useQuery({
    queryKey: queryKeys.mcp(orgId, projectId),
    queryFn: () => api<Page<MCPServer>>(apiPaths.mcpServers(orgId, projectId)),
    enabled: Boolean(orgId && projectId),
  });
}

export function useUnauthorized(error: unknown): boolean {
  return error instanceof Error && "status" in error && isUnauthorized(error);
}

export function useLiveConnection() {
  const _queryClient = useQueryClient();
  const [connected, setConnected] = useState(false);
  const _orgId = useWorkspace((state) => state.orgId);
  const _projectId = useWorkspace((state) => state.projectId);
  useEffect(() => {
    setConnected(false);
  }, []);
  return { connected, setConnected };
}
