import { useQuery } from "@tanstack/react-query";

import { api, apiPaths } from "@/lib/api";
import type {
  Agent,
  AgentVersion,
  Execution,
  Page,
  Workflow,
  WorkflowVersion,
} from "@/lib/api/types";

export type AgentCatalogRow = {
  agent: Agent;
  version: AgentVersion | null;
  executionCount: number;
  lastExecutionAt: string | null;
  workflowIds: string[];
};

export type WorkflowCatalogRow = {
  workflow: Workflow;
  version: WorkflowVersion | null;
  executions: Execution[];
  successRate: number | null;
  averageDurationMs: number | null;
  lastExecutionAt: string | null;
};

function stats(executions: Execution[]) {
  const terminal = executions.filter(
    (execution) =>
      execution.state === "succeeded" ||
      execution.state === "failed" ||
      execution.state === "cancelled",
  );
  const succeeded = terminal.filter((execution) => execution.state === "succeeded").length;
  const durations = terminal
    .map((execution) =>
      execution.started_at && execution.ended_at
        ? new Date(execution.ended_at).getTime() - new Date(execution.started_at).getTime()
        : null,
    )
    .filter((value): value is number => value !== null && value >= 0);
  return {
    successRate: terminal.length > 0 ? Math.round((succeeded / terminal.length) * 100) : null,
    averageDurationMs:
      durations.length > 0
        ? Math.round(durations.reduce((sum, value) => sum + value, 0) / durations.length)
        : null,
    lastExecutionAt:
      executions.length > 0
        ? executions.reduce(
            (latest, item) => (item.created_at > latest ? item.created_at : latest),
            "",
          )
        : null,
  };
}

export function useWorkflowCatalog(orgId: string, projectId: string, enabled = true) {
  return useQuery({
    queryKey: ["workflow-catalog", orgId, projectId],
    queryFn: async (): Promise<WorkflowCatalogRow[]> => {
      const [workflows, executions] = await Promise.all([
        api<Page<Workflow>>(apiPaths.workflows(orgId, projectId)),
        api<Page<Execution>>(`${apiPaths.executions(orgId, projectId)}?limit=200`),
      ]);
      return Promise.all(
        workflows.items.map(async (workflow) => {
          const executionsForWorkflow = executions.items.filter(
            (execution) => execution.workflow_id === workflow.id,
          );
          let version: WorkflowVersion | null = null;
          if (workflow.latest_version > 0) {
            version = await api<WorkflowVersion>(
              apiPaths.workflowVersion(orgId, projectId, workflow.id, workflow.latest_version),
            );
          }
          return {
            workflow,
            version,
            executions: executionsForWorkflow,
            ...stats(executionsForWorkflow),
          };
        }),
      );
    },
    enabled: enabled && Boolean(orgId && projectId),
  });
}

export function useAgentCatalog(orgId: string, projectId: string) {
  return useQuery({
    queryKey: ["agent-catalog", orgId, projectId],
    queryFn: async (): Promise<AgentCatalogRow[]> => {
      const [agents, workflows, executions] = await Promise.all([
        api<Page<Agent>>(apiPaths.agents(orgId, projectId)),
        api<Page<Workflow>>(apiPaths.workflows(orgId, projectId)),
        api<Page<Execution>>(`${apiPaths.executions(orgId, projectId)}?limit=200`),
      ]);
      const workflowAgents = await Promise.all(
        workflows.items.map(async (workflow) => {
          if (workflow.latest_version < 1)
            return { workflowId: workflow.id, agentIds: [] as string[] };
          const version = await api<WorkflowVersion>(
            apiPaths.workflowVersion(orgId, projectId, workflow.id, workflow.latest_version),
          );
          return {
            workflowId: workflow.id,
            agentIds: version.graph.nodes
              .filter((node) => node.type === "agent" || node.type === "llm")
              .map((node) => node.agent?.agent_id)
              .filter((value): value is string => Boolean(value)),
          };
        }),
      );
      return Promise.all(
        agents.items.map(async (agent) => {
          const versions = await api<Page<AgentVersion>>(
            apiPaths.agentVersions(orgId, projectId, agent.id),
          );
          const version =
            versions.items.find((item) => item.version === agent.latest_version) ??
            versions.items.at(0) ??
            null;
          const workflowIds = workflowAgents
            .filter((entry) => entry.agentIds.includes(agent.id))
            .map((entry) => entry.workflowId);
          const agentExecutions = executions.items.filter((execution) =>
            workflowIds.includes(execution.workflow_id),
          );
          return {
            agent,
            version,
            workflowIds,
            executionCount: agentExecutions.length,
            lastExecutionAt: stats(agentExecutions).lastExecutionAt,
          };
        }),
      );
    },
    enabled: Boolean(orgId && projectId),
  });
}
