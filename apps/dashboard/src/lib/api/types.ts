export type Page<T> = { items: T[]; next_cursor: string | null };

export type Organization = {
  id: string;
  name: string;
  slug: string;
  created_at: string;
};

export type Project = {
  id: string;
  org_id: string;
  name: string;
  slug: string;
  created_at: string;
  updated_at: string;
};

export type Environment = {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  slug: string;
  kind: "development" | "staging" | "production" | string;
  created_at: string;
  updated_at: string;
};

export type ExecutionCounts = {
  total: number;
  pending: number;
  assigned: number;
  running: number;
  succeeded: number;
  failed: number;
  cancelled: number;
  skipped: number;
};

export type Execution = {
  id: string;
  org_id: string;
  project_id: string;
  workflow_id: string;
  workflow_version_id: string;
  workflow_version: number;
  state: string;
  state_version: number;
  priority: number;
  counts: ExecutionCounts;
  input_digest: string;
  created_by: string;
  created_at: string;
  started_at: string | null;
  ended_at: string | null;
  deadline_at: string | null;
  cancel_reason: string | null;
  failure_code: string | null;
  failure_detail: string | null;
};

export type Task = {
  id: string;
  execution_id: string;
  node_id: string;
  node_type: string;
  state: string;
  state_version: number;
  priority: number;
  attempt_count: number;
  max_attempts: number;
  agent_version_id: string | null;
  tool_version_id: string | null;
  skip_reason: string | null;
  created_at: string;
  started_at: string | null;
  ended_at: string | null;
};

export type Attempt = {
  id: string;
  task_id: string;
  attempt_number: number;
  worker_id: string;
  outcome: string | null;
  outcome_unknown: boolean;
  effect_uncertain: boolean;
  error_code: string | null;
  error_message: string | null;
  dispatched_at: string;
  accepted_at: string | null;
  started_at: string | null;
  finished_at: string | null;
  result_available: boolean;
  checkpoint_available: boolean;
  metrics: Record<string, unknown>;
};

export type TaskResult = {
  outcome: string;
  output: unknown;
  error_code: string | null;
  error_message: string | null;
};

export type TaskDetail = Task & {
  attempts: Attempt[];
  result: TaskResult | null;
};

export type Event = {
  seq: number;
  event_id: string;
  event_type: string;
  entity_type: string;
  entity_id: string;
  actor_type: string;
  actor_id: string;
  from_state: string | null;
  to_state: string | null;
  reason: string | null;
  payload: Record<string, unknown>;
  created_at: string;
};

export type Agent = {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  description: string | null;
  latest_version: number;
  created_at: string;
  updated_at: string;
};

export type ToolBinding = {
  tool_id: string;
  version: number;
  config?: Record<string, unknown>;
  policy?: Record<string, unknown>;
};

export type MCPServerSpec = {
  name: string;
  transport: "stdio" | "streamable_http" | string;
  command?: string;
  args?: string[];
  url?: string;
  auth_credential_ref?: string;
  tool_policies?: Record<string, unknown>;
};

export type AgentSpec = {
  model?: {
    provider?: string;
    name?: string;
    credential_ref?: string;
    parameters?: Record<string, unknown>;
  };
  instructions?: string;
  tools?: ToolBinding[];
  runtime?: { abi_version?: number; image_digest?: string };
  limits?: { max_turns?: number; timeout_seconds?: number };
  checkpoint?: { mode?: string; require_checkpoint?: boolean };
  output_schema?: unknown;
  orchestration?: Record<string, unknown>;
  mcp_servers?: MCPServerSpec[];
};

export type AgentVersion = {
  id: string;
  agent_id: string;
  version: number;
  status: string;
  digest: string;
  spec: AgentSpec;
  runtime_abi: number;
  created_by: string;
  created_at: string;
  published_at: string | null;
};

export type Workflow = {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  description: string | null;
  latest_version: number;
  deployed_version: number | null;
  created_at: string;
  updated_at: string;
};

export type RetryPolicy = {
  max_attempts?: number;
  retry_on?: string[];
  backoff_base_ms?: number;
  backoff_max_ms?: number;
};

export type WorkflowNodeConfig = {
  expression?: string;
  mapping?: Record<string, string>;
  join?: { mode: "all" | "successful" | string; successful?: number; on_partial?: string };
  delay?: { seconds?: number; until?: string; event?: string };
  approval?: {
    prompt?: string;
    on_reject?: string;
    approvers?: string[];
    policy?: string;
    expires_in_seconds?: number;
    reject_reason_required?: boolean;
  };
  workflow?: { workflow_id: string; version: number };
  loop?: { max_iterations: number; exit_when?: string };
  input?: Record<string, string>;
  output_from?: string[];
};

export type WorkflowNode = {
  id: string;
  type: string;
  agent?: { agent_id: string; version: number };
  tool?: { tool_id: string; version: number };
  config?: WorkflowNodeConfig;
  params?: Record<string, unknown>;
  input_from?: string[];
  input_schema?: unknown;
  output_schema?: unknown;
  worker_pool?: string;
  timeout_seconds?: number;
  retry?: RetryPolicy;
  side_effect_class?: string;
  idempotency_key?: string;
  checkpoint_mode?: string;
  require_checkpoint?: boolean;
  resources?: { cpu_cores?: number; memory_mb?: number };
};

export type WorkflowEdge = {
  from: string;
  to: string;
  when?: string;
  data_mapping?: Record<string, string>;
  priority?: number;
};

export type WorkflowGraph = {
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  input_schema?: unknown;
  policy?: Record<string, unknown>;
};

export type WorkflowVersion = {
  id: string;
  workflow_id: string;
  version: number;
  status: string;
  digest: string;
  graph: WorkflowGraph;
  created_by: string;
  created_at: string;
  published_at: string | null;
};

export type Approval = {
  id: string;
  project_id?: string;
  execution_id?: string;
  task_id: string;
  prompt: string;
  policy: string;
  approvers: string[];
  status: string;
  requested_at: string;
  expires_at: string | null;
  decided_at: string | null;
  decided_by: string | null;
  reason: string | null;
};

export type Worker = {
  id: string;
  pool: string;
  status: string;
  max_concurrency: number;
  available_slots: number;
  reserved_slots: number;
  runtime: string;
  runtime_version: string;
  binary_version: string;
  cpu_cores: number;
  cpu_usage_percent: number | null;
  memory_mb: number;
  memory_used_mb: number | null;
  capabilities: string[];
  last_heartbeat_at: string;
};

export type Tool = {
  tool_id: string;
  name: string;
  version: number;
  description: string;
  source: string;
  schema?: unknown;
};

export type MCPServer = {
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  url?: string;
  auth_credential_ref?: string;
  tool_policies?: Record<string, unknown>;
  agents: { agent_id: string; agent_name: string; version: number }[];
};

export type UsageProject = {
  project_id: string;
  executions: number;
  succeeded: number;
  failed: number;
  attempts: number;
  tokens_in: number;
  tokens_out: number;
  duration_ms: number;
};

export type Usage = {
  from: string;
  to: string;
  projects: UsageProject[];
  total: UsageProject;
};

export type Meta = {
  name: string;
  environment: string;
  version: string;
  commit: string;
  build_date: string;
  time: string;
};
