"use client";

import { ArrowRight, CircleAlert, Plus, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";

import { nodeTypeLabel } from "@/components/builder/builder-node";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  useAgents,
  useAgentVersions,
  useTools,
  useWorkflows,
  useWorkflowVersions,
} from "@/lib/api/hooks";
import type { WorkflowEdge, WorkflowNode, WorkflowNodeConfig } from "@/lib/api/types";
import { cn } from "@/lib/utils";

const NODE_ID = /^[a-z][a-z0-9_]{0,63}$/;

type InspectorProps = {
  orgId: string;
  projectId: string;
  node: WorkflowNode | null;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  upstreamIds: string[];
  onChange: (node: WorkflowNode) => void;
  onRename: (from: string, to: string) => void;
  onAddEdge: (from: string, to: string, when?: string) => void;
  onPatchEdge: (from: string, to: string, patch: Partial<WorkflowEdge>) => void;
  onRemoveEdge: (from: string, to: string) => void;
  onDelete: () => void;
};

export function NodeInspector({
  orgId,
  projectId,
  node,
  nodes,
  edges,
  upstreamIds,
  onChange,
  onRename,
  onAddEdge,
  onPatchEdge,
  onRemoveEdge,
  onDelete,
}: InspectorProps) {
  const [idDraft, setIdDraft] = useState(node?.id ?? "");
  useEffect(() => setIdDraft(node?.id ?? ""), [node?.id]);

  if (!node) {
    return (
      <div className="p-4">
        <p className="text-sm font-medium">Select a node</p>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          Pick a node on the canvas to configure it. Drag from the node handles to connect steps.
        </p>
      </div>
    );
  }

  const config = node.config ?? {};
  const outgoing = edges.filter((edge) => edge.from === node.id);
  const workers = ["agent", "llm", "tool"].includes(node.type);
  const update = (patch: Partial<WorkflowNode>) => onChange({ ...node, ...patch });
  const updateConfig = (patch: Partial<WorkflowNodeConfig>) =>
    onChange({ ...node, config: { ...config, ...patch } });

  return (
    <div className="space-y-5 p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            {nodeTypeLabel(node.type)}
          </p>
          <p className="truncate font-mono text-sm font-medium">{node.id}</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          onClick={onDelete}
          aria-label={`Delete ${node.id}`}
          className="text-muted-foreground hover:text-danger"
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>

      <section className="space-y-2">
        <label htmlFor="builder-node-id" className="text-xs font-medium">
          Node ID
        </label>
        <Input
          id="builder-node-id"
          value={idDraft}
          onChange={(event) => setIdDraft(event.target.value)}
          onBlur={() => {
            const next = idDraft.trim();
            if (next && next !== node.id && NODE_ID.test(next)) onRename(node.id, next);
            else setIdDraft(node.id);
          }}
          className="h-8 font-mono text-xs"
          spellCheck={false}
        />
        {idDraft && !NODE_ID.test(idDraft) ? (
          <p className="text-[11px] text-danger">
            Use lowercase letters, digits and underscores; start with a letter.
          </p>
        ) : null}
        <p className="text-[11px] text-muted-foreground">
          Renaming updates every edge, input reference and downstream mapping automatically.
        </p>
      </section>

      {workers ? (
        <WorkerFields
          orgId={orgId}
          projectId={projectId}
          node={node}
          update={update}
          upstreamIds={upstreamIds}
        />
      ) : null}

      {node.type === "condition" ? (
        <section className="space-y-2">
          <label htmlFor="condition-expression" className="text-xs font-medium">
            Expression (CEL)
          </label>
          <textarea
            id="condition-expression"
            value={config.expression ?? ""}
            onChange={(event) => updateConfig({ expression: event.target.value })}
            rows={3}
            spellCheck={false}
            placeholder="input.score >= 80"
            className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
          />
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            The node stores <span className="font-mono">{"{ result: bool }"}</span>. Branch edges
            compare
            <span className="font-mono"> outputs.{node.id}.result</span>.
          </p>
        </section>
      ) : null}

      {node.type === "transform" ? (
        <TransformFields node={node} config={config} updateConfig={updateConfig} />
      ) : null}

      {node.type === "join" ? (
        <section className="space-y-3">
          <h3 className="text-xs font-medium">Join policy</h3>
          <Field label="Mode">
            <Select
              value={config.join?.mode ?? "all"}
              onChange={(event) =>
                updateConfig({
                  join: {
                    ...config.join,
                    mode: event.target.value,
                    successful:
                      event.target.value === "successful"
                        ? (config.join?.successful ?? 1)
                        : undefined,
                  },
                })
              }
              className="w-full"
            >
              <option value="all">Wait for all branches</option>
              <option value="successful">Wait for N successes</option>
            </Select>
          </Field>
          {config.join?.mode === "successful" ? (
            <Field label="Successful branches">
              <Input
                type="number"
                min={1}
                value={config.join?.successful ?? 1}
                onChange={(event) =>
                  updateConfig({
                    join: {
                      ...config.join,
                      mode: "successful",
                      successful: Number(event.target.value),
                    },
                  })
                }
                className="h-8 text-xs"
              />
            </Field>
          ) : null}
          <Field label="When a branch fails">
            <Select
              value={config.join?.on_partial ?? "fail"}
              onChange={(event) =>
                updateConfig({
                  join: {
                    ...config.join,
                    mode: config.join?.mode ?? "all",
                    on_partial: event.target.value,
                  },
                })
              }
              className="w-full"
            >
              <option value="fail">Fail the join</option>
              <option value="skip">Skip the join</option>
              <option value="continue">Continue with available branches</option>
            </Select>
          </Field>
        </section>
      ) : null}

      {node.type === "delay" ? <DelayFields config={config} updateConfig={updateConfig} /> : null}
      {node.type === "approval" ? (
        <ApprovalFields node={node} config={config} updateConfig={updateConfig} />
      ) : null}
      {node.type === "subworkflow" || node.type === "loop" ? (
        <SubworkflowFields
          orgId={orgId}
          projectId={projectId}
          node={node}
          config={config}
          updateConfig={updateConfig}
        />
      ) : null}

      {workers ? (
        <>
          <InputFrom node={node} nodes={nodes} upstreamIds={upstreamIds} update={update} />
          <JsonField
            label="Output schema"
            description="Validated by the runtime before the output becomes visible downstream."
            value={node.output_schema}
            onChange={(value) => update({ output_schema: value ?? undefined })}
            rows={5}
          />
        </>
      ) : null}

      {node.type === "transform" ? (
        <JsonField
          label="Output schema"
          description="Optional schema for the transform result."
          value={node.output_schema}
          onChange={(value) => update({ output_schema: value ?? undefined })}
          rows={4}
        />
      ) : null}

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-medium">Outgoing edges</h3>
          <Badge variant="outline">{outgoing.length}</Badge>
        </div>
        {outgoing.length === 0 ? (
          <p className="rounded-md border border-dashed border-border p-2 text-[11px] text-muted-foreground">
            Drag from this node to another node to create an edge.
          </p>
        ) : (
          <ul className="space-y-2">
            {outgoing.map((edge) => (
              <EdgeEditor
                key={`${edge.from}-${edge.to}`}
                edge={edge}
                nodes={nodes}
                onPatch={(patch) => onPatchEdge(edge.from, edge.to, patch)}
                onRemove={() => onRemoveEdge(edge.from, edge.to)}
              />
            ))}
          </ul>
        )}
        <AddEdge
          source={node.id}
          nodes={nodes}
          nodesWithEdge={new Set(outgoing.map((edge) => edge.to))}
          condition={node.type === "condition"}
          branchCount={outgoing.length}
          onAdd={onAddEdge}
        />
      </section>

      <JsonField
        label="Retry policy"
        description="Overrides the workflow policy for this node."
        value={node.retry}
        onChange={(value) => update({ retry: value as WorkflowNode["retry"] })}
        rows={4}
      />
      <JsonField
        label="Resources"
        description="Optional worker placement bounds."
        value={node.resources}
        onChange={(value) => update({ resources: value as WorkflowNode["resources"] })}
        rows={3}
      />
      <JsonField
        label="Raw node JSON"
        description="Advanced escape hatch. Changing this replaces the whole node definition."
        value={node}
        onChange={(value) => {
          if (value && typeof value === "object" && "id" in value && "type" in value) {
            onChange(value as WorkflowNode);
          }
        }}
        rows={10}
      />
    </div>
  );
}

function WorkerFields({
  orgId,
  projectId,
  node,
  update,
  upstreamIds,
}: {
  orgId: string;
  projectId: string;
  node: WorkflowNode;
  update: (patch: Partial<WorkflowNode>) => void;
  upstreamIds: string[];
}) {
  const agents = useAgents(orgId, projectId);
  const tools = useTools(orgId, projectId);
  const versions = useAgentVersions(orgId, projectId, node.agent?.agent_id ?? "");
  const selectedAgent = (agents.data?.items ?? []).find(
    (agent) => agent.id === node.agent?.agent_id,
  );
  const selectedVersion = (versions.data?.items ?? []).find(
    (version) => version.version === node.agent?.version,
  );

  if (node.type === "tool") {
    return (
      <section className="space-y-3">
        <h3 className="text-xs font-medium">Tool</h3>
        <Field label="Tool">
          <Select
            value={node.tool?.tool_id ?? ""}
            onChange={(event) => {
              const tool = (tools.data?.items ?? []).find(
                (item) => item.tool_id === event.target.value,
              );
              update({ tool: tool ? { tool_id: tool.tool_id, version: tool.version } : undefined });
            }}
            className="w-full"
          >
            <option value="">Select a tool</option>
            {(tools.data?.items ?? []).map((tool) => (
              <option key={tool.tool_id} value={tool.tool_id}>
                {tool.name} · v{tool.version}
              </option>
            ))}
          </Select>
        </Field>
        <JsonField
          label="Parameters"
          description="Runtime parameters passed as node.params."
          value={node.params}
          onChange={(value) => update({ params: (value as Record<string, unknown>) ?? undefined })}
          rows={5}
        />
        <div className="grid grid-cols-2 gap-2">
          <Field label="Timeout (s)">
            <Input
              type="number"
              min={1}
              max={86_400}
              value={node.timeout_seconds ?? 120}
              onChange={(event) => update({ timeout_seconds: Number(event.target.value) })}
              className="h-8 text-xs"
            />
          </Field>
          <Field label="Worker pool">
            <Input
              value={node.worker_pool ?? "default"}
              onChange={(event) => update({ worker_pool: event.target.value })}
              className="h-8 text-xs"
            />
          </Field>
        </div>
        <RetryFields node={node} update={update} />
      </section>
    );
  }

  return (
    <section className="space-y-3">
      <h3 className="text-xs font-medium">{node.type === "llm" ? "LLM agent" : "Agent"}</h3>
      <Field label="Agent">
        <Select
          value={node.agent?.agent_id ?? ""}
          onChange={(event) => {
            const agent = (agents.data?.items ?? []).find((item) => item.id === event.target.value);
            update({
              agent: agent ? { agent_id: agent.id, version: agent.latest_version } : undefined,
            });
          }}
          className="w-full"
        >
          <option value="">Select an agent</option>
          {(agents.data?.items ?? []).map((agent) => (
            <option key={agent.id} value={agent.id}>
              {agent.name} · v{agent.latest_version}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Version">
        <Select
          value={String(node.agent?.version ?? "")}
          onChange={(event) =>
            update({
              agent: { agent_id: node.agent?.agent_id ?? "", version: Number(event.target.value) },
            })
          }
          className="w-full"
          disabled={!node.agent?.agent_id}
        >
          <option value="">Select a version</option>
          {(versions.data?.items ?? []).map((version) => (
            <option key={version.id} value={version.version}>
              v{version.version} · {version.status}
            </option>
          ))}
        </Select>
      </Field>
      {selectedAgent && selectedVersion ? (
        <p className="rounded-md border border-border bg-muted/30 p-2 text-[11px] text-muted-foreground">
          {selectedVersion.spec.model?.provider ?? "provider not set"} /{" "}
          {selectedVersion.spec.model?.name ?? "model not set"} · runtime ABI{" "}
          {selectedVersion.runtime_abi}
        </p>
      ) : null}
      <div className="grid grid-cols-2 gap-2">
        <Field label="Timeout (s)">
          <Input
            type="number"
            min={1}
            max={86_400}
            value={node.timeout_seconds ?? 300}
            onChange={(event) => update({ timeout_seconds: Number(event.target.value) })}
            className="h-8 text-xs"
          />
        </Field>
        <Field label="Worker pool">
          <Input
            value={node.worker_pool ?? "default"}
            onChange={(event) => update({ worker_pool: event.target.value })}
            className="h-8 text-xs"
          />
        </Field>
      </div>
      <RetryFields node={node} update={update} />
      <JsonField
        label="Parameters"
        description="Extra node params passed to the runtime."
        value={node.params}
        onChange={(value) => update({ params: (value as Record<string, unknown>) ?? undefined })}
        rows={4}
      />
      <div className="space-y-1">
        <span className="block text-[11px] text-muted-foreground">Input mapping</span>
        <p className="text-[11px] text-muted-foreground">
          Upstream outputs available: {upstreamIds.length === 0 ? "none" : upstreamIds.join(", ")}
        </p>
      </div>
      <p className="text-[11px] text-muted-foreground">
        Output mapping is performed by downstream transform nodes or edge data mappings.
      </p>
    </section>
  );
}

function RetryFields({
  node,
  update,
}: {
  node: WorkflowNode;
  update: (patch: Partial<WorkflowNode>) => void;
}) {
  const retry = node.retry ?? {};
  return (
    <>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Max attempts">
          <Input
            type="number"
            min={1}
            max={10}
            value={retry.max_attempts ?? 1}
            onChange={(event) =>
              update({ retry: { ...retry, max_attempts: Number(event.target.value) } })
            }
            className="h-8 text-xs"
          />
        </Field>
        <Field label="Backoff (ms)">
          <Input
            type="number"
            min={100}
            value={retry.backoff_base_ms ?? 100}
            onChange={(event) =>
              update({ retry: { ...retry, backoff_base_ms: Number(event.target.value) } })
            }
            className="h-8 text-xs"
          />
        </Field>
      </div>
      <Field label="Retry on">
        <Input
          value={(retry.retry_on ?? []).join(",")}
          onChange={(event) =>
            update({
              retry: {
                ...retry,
                retry_on: event.target.value
                  .split(",")
                  .map((item) => item.trim())
                  .filter(Boolean),
              },
            })
          }
          placeholder="transient,timeout,worker_lost"
          className="h-8 font-mono text-xs"
        />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Side effect">
          <Select
            value={node.side_effect_class ?? "read_only"}
            onChange={(event) => update({ side_effect_class: event.target.value })}
            className="w-full"
          >
            <option value="read_only">read_only</option>
            <option value="idempotent">idempotent</option>
            <option value="non_idempotent">non_idempotent</option>
          </Select>
        </Field>
        <Field label="Checkpoint">
          <Select
            value={node.checkpoint_mode ?? "inherit"}
            onChange={(event) => update({ checkpoint_mode: event.target.value })}
            className="w-full"
          >
            <option value="inherit">inherit</option>
            <option value="on_node_end">on_node_end</option>
            <option value="none">none</option>
          </Select>
        </Field>
      </div>
      {node.side_effect_class === "non_idempotent" && (retry.max_attempts ?? 1) > 1 ? (
        <Field label="Idempotency key">
          <Input
            value={node.idempotency_key ?? ""}
            onChange={(event) => update({ idempotency_key: event.target.value })}
            className="h-8 font-mono text-xs"
          />
        </Field>
      ) : null}
    </>
  );
}

function TransformFields({
  node,
  config,
  updateConfig,
}: {
  node: WorkflowNode;
  config: WorkflowNodeConfig;
  updateConfig: (patch: Partial<WorkflowNodeConfig>) => void;
}) {
  const mapping = config.mapping ?? {};
  const rows = Object.entries(mapping);
  const setRows = (next: [string, string][]) =>
    updateConfig({ mapping: Object.fromEntries(next.filter(([key]) => key.trim())) });
  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-xs font-medium">Output mapping</h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setRows([...rows, [nextMappingKey("field", rows), ""]])}
        >
          <Plus className="size-3.5" /> Add
        </Button>
      </div>
      {rows.length === 0 ? (
        <p className="rounded-md border border-dashed border-border p-2 text-[11px] text-muted-foreground">
          Map target paths to CEL expressions, for example{" "}
          <span className="font-mono">summary</span> →{" "}
          <span className="font-mono">outputs.research.report</span>.
        </p>
      ) : null}
      <ul className="space-y-2">
        {rows.map(([target, expression], index) => (
          <li key={target} className="space-y-1.5 rounded-md border border-border p-2">
            <div className="flex gap-1.5">
              <Input
                value={target}
                onChange={(event) => {
                  const next = [...rows] as [string, string][];
                  next[index] = [event.target.value, expression];
                  setRows(next);
                }}
                placeholder="target.path"
                className="h-7 font-mono text-[11px]"
                aria-label="Mapping target"
              />
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setRows(rows.filter((_, row) => row !== index))}
                aria-label="Remove mapping"
              >
                <X className="size-3.5" />
              </Button>
            </div>
            <Input
              value={expression}
              onChange={(event) => {
                const next = [...rows] as [string, string][];
                next[index] = [target, event.target.value];
                setRows(next);
              }}
              placeholder="outputs.research.report"
              className="h-7 font-mono text-[11px]"
              aria-label="Mapping expression"
            />
          </li>
        ))}
      </ul>
      <p className="text-[11px] text-muted-foreground">
        Transform node <span className="font-mono">{node.id}</span> writes every target path into
        its output.
      </p>
    </section>
  );
}

function DelayFields({
  config,
  updateConfig,
}: {
  config: WorkflowNodeConfig;
  updateConfig: (patch: Partial<WorkflowNodeConfig>) => void;
}) {
  const delay = config.delay ?? {};
  const mode = delay.event ? "event" : delay.until ? "until" : "seconds";
  return (
    <section className="space-y-3">
      <h3 className="text-xs font-medium">Durable delay</h3>
      <Field label="Wait for">
        <Select
          value={mode}
          onChange={(event) => {
            if (event.target.value === "seconds") updateConfig({ delay: { seconds: 60 } });
            else if (event.target.value === "until")
              updateConfig({ delay: { until: new Date().toISOString() } });
            else updateConfig({ delay: { event: "release.approved" } });
          }}
          className="w-full"
        >
          <option value="seconds">Duration</option>
          <option value="until">Timestamp</option>
          <option value="event">External event</option>
        </Select>
      </Field>
      {mode === "seconds" ? (
        <Field label="Seconds">
          <Input
            type="number"
            min={1}
            max={604_800}
            value={delay.seconds ?? 60}
            onChange={(event) => updateConfig({ delay: { seconds: Number(event.target.value) } })}
            className="h-8 text-xs"
          />
        </Field>
      ) : null}
      {mode === "until" ? (
        <Field label="Until (RFC3339)">
          <Input
            value={delay.until ?? ""}
            onChange={(event) => updateConfig({ delay: { until: event.target.value } })}
            className="h-8 font-mono text-xs"
          />
        </Field>
      ) : null}
      {mode === "event" ? (
        <Field label="Event name">
          <Input
            value={delay.event ?? ""}
            onChange={(event) => updateConfig({ delay: { event: event.target.value } })}
            className="h-8 font-mono text-xs"
          />
        </Field>
      ) : null}
      <p className="text-[11px] text-muted-foreground">Delay nodes hold no worker slot.</p>
    </section>
  );
}

function ApprovalFields({
  node,
  config,
  updateConfig,
}: {
  node: WorkflowNode;
  config: WorkflowNodeConfig;
  updateConfig: (patch: Partial<WorkflowNodeConfig>) => void;
}) {
  const approval = config.approval ?? {};
  return (
    <section className="space-y-3">
      <h3 className="text-xs font-medium">Approval</h3>
      <Field label="Prompt">
        <textarea
          value={approval.prompt ?? ""}
          onChange={(event) =>
            updateConfig({ approval: { ...approval, prompt: event.target.value } })
          }
          rows={2}
          className="w-full rounded-md border border-input bg-background p-2 text-xs"
        />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Policy">
          <Select
            value={approval.policy ?? "any"}
            onChange={(event) =>
              updateConfig({ approval: { ...approval, policy: event.target.value } })
            }
            className="w-full"
          >
            <option value="any">Any approver</option>
            <option value="all">All approvers</option>
          </Select>
        </Field>
        <Field label="On reject">
          <Select
            value={approval.on_reject ?? "fail"}
            onChange={(event) =>
              updateConfig({ approval: { ...approval, on_reject: event.target.value } })
            }
            className="w-full"
          >
            <option value="fail">Fail</option>
            <option value="skip">Skip</option>
            <option value="continue">Continue</option>
          </Select>
        </Field>
      </div>
      <Field label="Approvers (comma separated)">
        <Input
          value={(approval.approvers ?? []).join(",")}
          onChange={(event) =>
            updateConfig({
              approval: {
                ...approval,
                approvers: event.target.value
                  .split(",")
                  .map((item) => item.trim())
                  .filter(Boolean),
              },
            })
          }
          placeholder="alice,bob"
          className="h-8 font-mono text-xs"
        />
      </Field>
      <Field label="Expires in seconds">
        <Input
          type="number"
          min={1}
          max={604_800}
          value={approval.expires_in_seconds ?? ""}
          onChange={(event) =>
            updateConfig({
              approval: {
                ...approval,
                expires_in_seconds: event.target.value ? Number(event.target.value) : undefined,
              },
            })
          }
          className="h-8 text-xs"
        />
      </Field>
      <p className="text-[11px] text-muted-foreground">
        Approval <span className="font-mono">{node.id}</span> pauses the execution without holding a
        worker.
      </p>
    </section>
  );
}

function SubworkflowFields({
  orgId,
  projectId,
  node,
  config,
  updateConfig,
}: {
  orgId: string;
  projectId: string;
  node: WorkflowNode;
  config: WorkflowNodeConfig;
  updateConfig: (patch: Partial<WorkflowNodeConfig>) => void;
}) {
  const workflows = useWorkflows(orgId, projectId);
  const workflowId = config.workflow?.workflow_id ?? "";
  const versions = useWorkflowVersions(orgId, projectId, workflowId);
  const mapping = config.input ?? {};
  const rows = Object.entries(mapping);
  const setRows = (next: [string, string][]) =>
    updateConfig({ input: Object.fromEntries(next.filter(([key]) => key.trim())) });

  return (
    <section className="space-y-3">
      <h3 className="text-xs font-medium">{node.type === "loop" ? "Loop child" : "Subworkflow"}</h3>
      <Field label="Workflow">
        <Select
          value={workflowId}
          onChange={(event) => {
            const workflow = (workflows.data?.items ?? []).find(
              (item) => item.id === event.target.value,
            );
            updateConfig({
              workflow: workflow
                ? { workflow_id: workflow.id, version: workflow.latest_version }
                : { workflow_id: "", version: 1 },
            });
          }}
          className="w-full"
        >
          <option value="">Select a workflow</option>
          {(workflows.data?.items ?? []).map((workflow) => (
            <option key={workflow.id} value={workflow.id}>
              {workflow.name} · v{workflow.latest_version}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Version">
        <Select
          value={String(config.workflow?.version ?? "")}
          onChange={(event) =>
            updateConfig({
              workflow: { workflow_id: workflowId, version: Number(event.target.value) },
            })
          }
          className="w-full"
          disabled={!workflowId}
        >
          <option value="">Select a version</option>
          {(versions.data?.items ?? []).map((version) => (
            <option key={version.id} value={version.version}>
              v{version.version} · {version.status}
            </option>
          ))}
        </Select>
      </Field>

      {node.type === "loop" ? (
        <>
          <Field label="Max iterations">
            <Input
              type="number"
              min={1}
              max={100}
              value={config.loop?.max_iterations ?? 3}
              onChange={(event) =>
                updateConfig({
                  loop: { ...config.loop, max_iterations: Number(event.target.value) },
                })
              }
              className="h-8 text-xs"
            />
          </Field>
          <Field label="Exit when (CEL)">
            <Input
              value={config.loop?.exit_when ?? ""}
              onChange={(event) =>
                updateConfig({
                  loop: {
                    ...config.loop,
                    max_iterations: config.loop?.max_iterations ?? 3,
                    exit_when: event.target.value,
                  },
                })
              }
              placeholder="outputs.loop.status == 'done'"
              className="h-8 font-mono text-xs"
            />
          </Field>
        </>
      ) : null}

      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium">Child input mapping</h4>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setRows([...rows, [nextMappingKey("input", rows), ""]])}
        >
          <Plus className="size-3.5" /> Add
        </Button>
      </div>
      {rows.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          No mapping: the child receives the resolved node input.
        </p>
      ) : null}
      {rows.map(([target, expression], index) => (
        <div key={target} className="space-y-1.5 rounded-md border border-border p-2">
          <div className="flex gap-1.5">
            <Input
              value={target}
              onChange={(event) => {
                const next = [...rows] as [string, string][];
                next[index] = [event.target.value, expression];
                setRows(next);
              }}
              placeholder="topic"
              className="h-7 font-mono text-[11px]"
              aria-label="Child input target"
            />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setRows(rows.filter((_, row) => row !== index))}
              aria-label="Remove mapping"
            >
              <X className="size-3.5" />
            </Button>
          </div>
          <Input
            value={expression}
            onChange={(event) => {
              const next = [...rows] as [string, string][];
              next[index] = [target, event.target.value];
              setRows(next);
            }}
            placeholder="input.topic"
            className="h-7 font-mono text-[11px]"
            aria-label="Child input expression"
          />
        </div>
      ))}
    </section>
  );
}

function InputFrom({
  node,
  nodes,
  upstreamIds,
  update,
}: {
  node: WorkflowNode;
  nodes: WorkflowNode[];
  upstreamIds: string[];
  update: (patch: Partial<WorkflowNode>) => void;
}) {
  const selected = new Set(node.input_from ?? []);
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-medium">Input from</h3>
      <p className="text-[11px] text-muted-foreground">
        Limit which direct upstream outputs the node receives.
      </p>
      <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border border-border p-2">
        {upstreamIds.length === 0 ? (
          <p className="text-[11px] text-muted-foreground">No upstream nodes yet.</p>
        ) : (
          upstreamIds.map((id) => {
            const target = nodes.find((item) => item.id === id);
            return (
              <label key={id} className="flex items-center gap-2 text-[11px]">
                <input
                  type="checkbox"
                  checked={selected.has(id)}
                  onChange={(event) => {
                    const next = new Set(selected);
                    if (event.target.checked) next.add(id);
                    else next.delete(id);
                    update({ input_from: next.size > 0 ? [...next] : undefined });
                  }}
                  className="size-3.5 rounded border-border"
                />
                <span className="font-mono">{id}</span>
                <span className="text-muted-foreground">{target?.type}</span>
              </label>
            );
          })
        )}
      </div>
    </section>
  );
}

function EdgeEditor({
  edge,
  nodes,
  onPatch,
  onRemove,
}: {
  edge: WorkflowEdge;
  nodes: WorkflowNode[];
  onPatch: (patch: Partial<WorkflowEdge>) => void;
  onRemove: () => void;
}) {
  const target = nodes.find((node) => node.id === edge.to);
  const [mapping, setMapping] = useState(
    edge.data_mapping ? JSON.stringify(edge.data_mapping, null, 2) : "",
  );
  const [mappingError, setMappingError] = useState<string | null>(null);
  useEffect(() => {
    setMapping(edge.data_mapping ? JSON.stringify(edge.data_mapping, null, 2) : "");
    setMappingError(null);
  }, [edge.data_mapping]);
  return (
    <li className="space-y-2 rounded-md border border-border p-2">
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1 text-[11px]">
          <ArrowRight className="size-3 shrink-0 text-muted-foreground" />
          <span className="truncate font-mono">{edge.to}</span>
          <span className="shrink-0 text-muted-foreground">{target?.type}</span>
        </span>
        <Button
          variant="ghost"
          size="icon"
          onClick={onRemove}
          aria-label={`Remove edge to ${edge.to}`}
        >
          <X className="size-3.5" />
        </Button>
      </div>
      <Input
        value={edge.when ?? ""}
        onChange={(event) => onPatch({ when: event.target.value || undefined })}
        placeholder="when (CEL), leave empty to always follow"
        className="h-7 font-mono text-[11px]"
        aria-label={`Edge condition to ${edge.to}`}
      />
      <Input
        type="number"
        min={-100}
        max={100}
        value={edge.priority ?? 0}
        onChange={(event) => onPatch({ priority: Number(event.target.value) })}
        className="h-7 text-[11px]"
        aria-label={`Edge priority to ${edge.to}`}
      />
      <details>
        <summary className="cursor-pointer text-[10px] text-muted-foreground hover:text-foreground">
          Data mapping
        </summary>
        <textarea
          value={mapping}
          onChange={(event) => {
            setMapping(event.target.value);
            setMappingError(null);
          }}
          onBlur={() => {
            if (!mapping.trim()) {
              onPatch({ data_mapping: undefined });
              return;
            }
            try {
              const parsed = JSON.parse(mapping) as unknown;
              if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
                throw new Error("must be an object");
              onPatch({ data_mapping: parsed as Record<string, string> });
            } catch (error) {
              setMappingError(error instanceof Error ? error.message : "invalid JSON");
            }
          }}
          rows={3}
          spellCheck={false}
          placeholder={'{\n  "target": "outputs.source.field"\n}'}
          className="mt-1 w-full rounded-md border border-input bg-background p-2 font-mono text-[11px]"
        />
        {mappingError ? <p className="mt-1 text-[10px] text-danger">{mappingError}</p> : null}
      </details>
    </li>
  );
}

function AddEdge({
  source,
  nodes,
  nodesWithEdge,
  condition,
  branchCount,
  onAdd,
}: {
  source: string;
  nodes: WorkflowNode[];
  nodesWithEdge: Set<string>;
  condition: boolean;
  branchCount: number;
  onAdd: (from: string, to: string, when?: string) => void;
}) {
  const [target, setTarget] = useState("");
  const options = nodes.filter((node) => node.id !== source && !nodesWithEdge.has(node.id));
  return (
    <div className="space-y-1.5 rounded-md border border-dashed border-border p-2">
      <div className="flex gap-1.5">
        <Select
          value={target}
          onChange={(event) => setTarget(event.target.value)}
          aria-label="New edge target"
          className="min-w-0 flex-1"
        >
          <option value="">Connect to…</option>
          {options.map((node) => (
            <option key={node.id} value={node.id}>
              {node.id} · {node.type}
            </option>
          ))}
        </Select>
        <Button
          size="sm"
          variant="outline"
          disabled={!target}
          onClick={() => {
            if (!target) return;
            const when =
              condition && branchCount === 0
                ? `outputs.${source}.result == true`
                : condition && branchCount === 1
                  ? `outputs.${source}.result == false`
                  : undefined;
            onAdd(source, target, when);
            setTarget("");
          }}
        >
          Add
        </Button>
      </div>
      {condition ? (
        <p className="text-[10px] text-muted-foreground">
          Condition branches compare <span className="font-mono">outputs.{source}.result</span>; the
          first two default to true and false.
        </p>
      ) : null}
    </div>
  );
}

function JsonField({
  label,
  description,
  value,
  onChange,
  rows = 4,
}: {
  label: string;
  description?: string;
  value: unknown;
  onChange: (value: unknown) => void;
  rows?: number;
}) {
  const [draft, setDraft] = useState(() =>
    value === undefined ? "" : JSON.stringify(value, null, 2),
  );
  const [error, setError] = useState<string | null>(null);
  const [focused, setFocused] = useState(false);
  useEffect(() => {
    if (!focused) {
      setDraft(value === undefined ? "" : JSON.stringify(value, null, 2));
      setError(null);
    }
  }, [focused, value]);
  return (
    <section className="space-y-1.5">
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-medium">{label}</h3>
        {error ? <CircleAlert className="size-3.5 text-danger" aria-label="Invalid JSON" /> : null}
      </div>
      {description ? <p className="text-[11px] text-muted-foreground">{description}</p> : null}
      <textarea
        value={draft}
        onChange={(event) => {
          setDraft(event.target.value);
          setError(null);
        }}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false);
          if (!draft.trim()) {
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(draft));
            setError(null);
          } catch (error) {
            setError(error instanceof Error ? error.message : "invalid JSON");
          }
        }}
        rows={rows}
        spellCheck={false}
        placeholder="{}"
        className={cn(
          "scrollbar-thin w-full rounded-md border bg-background p-2 font-mono text-[11px]",
          error ? "border-danger" : "border-input",
        )}
      />
    </section>
  );
}

function nextMappingKey(prefix: string, rows: [string, string][]): string {
  let index = rows.length + 1;
  let candidate = `${prefix}_${index}`;
  while (rows.some(([target]) => target === candidate)) {
    index += 1;
    candidate = `${prefix}_${index}`;
  }
  return candidate;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    // biome-ignore lint/a11y/noLabelWithoutControl: The caller supplies a form control inside the label.
    <label className="block space-y-1">
      <span className="block text-[11px] text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}
