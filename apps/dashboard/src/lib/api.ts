import { useWorkspace } from "@/stores/workspace";

const BASE = "/api/platform";

export class ApiError extends Error {
  status: number;
  code: string;
  details: unknown;
  requestId: string | null;

  constructor(
    status: number,
    code: string,
    message: string,
    details?: unknown,
    requestId?: string,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId ?? null;
  }
}

export function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}

async function parseError(response: Response): Promise<ApiError> {
  let payload: Record<string, unknown> = {};
  try {
    payload = (await response.json()) as Record<string, unknown>;
  } catch {
    // Non-JSON error (proxy failure, HTML page).
  }
  const error = payload.error as Record<string, unknown> | undefined;
  return new ApiError(
    response.status,
    String(error?.code ?? "request_failed"),
    String(error?.message ?? response.statusText ?? "Request failed"),
    error?.details,
    typeof error?.request_id === "string" ? error.request_id : undefined,
  );
}

export async function api<T>(
  path: string,
  options: RequestInit & { json?: unknown; idempotencyKey?: string } = {},
): Promise<T> {
  const { json, idempotencyKey, ...init } = options;
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (json !== undefined) headers.set("Content-Type", "application/json");
  if (idempotencyKey) headers.set("Idempotency-Key", idempotencyKey);
  const apiKey = useWorkspace.getState().apiKey;
  if (apiKey) headers.set("X-Dashboard-Api-Key", apiKey);

  const response = await fetch(`${BASE}/${path}`, {
    ...init,
    headers,
    body: json !== undefined ? JSON.stringify(json) : init.body,
    cache: "no-store",
  });
  if (!response.ok) throw await parseError(response);
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

export type StreamEvent = {
  seq: number;
  event_type: string;
  entity_type: string;
  entity_id: string;
  to_state?: string | null;
  [key: string]: unknown;
};

export function newIdempotencyKey(prefix = "dashboard"): string {
  return `${prefix}-${crypto.randomUUID().replaceAll("-", "")}`;
}

export const apiPaths = {
  organizations: () => "organizations",
  projects: (orgId: string) => `organizations/${encodeURIComponent(orgId)}/projects`,
  environments: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/environments`,
  agents: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/agents`,
  agentVersions: (orgId: string, projectId: string, agentId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/agents/${encodeURIComponent(agentId)}/versions`,
  agentVersion: (orgId: string, projectId: string, agentId: string, version: number) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/agents/${encodeURIComponent(agentId)}/versions/${version}`,
  workflows: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/workflows`,
  validateWorkflow: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/workflows:validate`,
  workflowVersions: (orgId: string, projectId: string, workflowId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/workflows/${encodeURIComponent(workflowId)}/versions`,
  workflowVersion: (orgId: string, projectId: string, workflowId: string, version: number) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/workflows/${encodeURIComponent(workflowId)}/versions/${version}`,
  executions: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions`,
  execution: (orgId: string, projectId: string, executionId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}`,
  executionInput: (orgId: string, projectId: string, executionId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/input`,
  tasks: (orgId: string, projectId: string, executionId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/tasks`,
  task: (orgId: string, projectId: string, executionId: string, taskId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/tasks/${encodeURIComponent(taskId)}`,
  events: (orgId: string, projectId: string, executionId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/events`,
  replay: (orgId: string, projectId: string, executionId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/replay`,
  stream: (orgId: string, projectId: string, executionId: string, after: number) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/executions/${encodeURIComponent(executionId)}/stream?after=${after}`,
  approvals: (orgId: string, query = "") =>
    `organizations/${encodeURIComponent(orgId)}/approvals${query ? `?${query}` : ""}`,
  usage: (orgId: string, query = "") =>
    `organizations/${encodeURIComponent(orgId)}/usage${query ? `?${query}` : ""}`,
  workers: (orgId: string, query = "") =>
    `organizations/${encodeURIComponent(orgId)}/workers${query ? `?${query}` : ""}`,
  tools: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/tools`,
  mcpServers: (orgId: string, projectId: string) =>
    `organizations/${encodeURIComponent(orgId)}/projects/${encodeURIComponent(projectId)}/mcp/servers`,
  meta: () => "meta",
};
