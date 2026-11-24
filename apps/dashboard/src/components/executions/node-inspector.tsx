"use client";

import { AlertTriangle, Bot, CheckCircle2, Clock3, Cpu, Database, Wrench } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAgentVersion, useTaskDetail } from "@/lib/api/hooks";
import type { Execution, WorkflowNode } from "@/lib/api/types";
import { formatCost, formatDurationBetween } from "@/lib/format";
import { redactedJson } from "@/lib/redact";
import { formatDateTime, formatDuration } from "@/lib/utils";

function metric(metrics: Record<string, unknown> | undefined, key: string): number {
  const value = metrics?.[key];
  return typeof value === "number" ? value : 0;
}

export function NodeInspector({
  orgId,
  projectId,
  execution,
  node,
  taskId,
  executionInput,
  nodeInputs,
}: {
  orgId: string;
  projectId: string;
  execution: Execution;
  node: WorkflowNode | undefined;
  taskId: string | null;
  executionInput: unknown;
  nodeInputs: Record<string, unknown>;
}) {
  const detail = useTaskDetail(orgId, projectId, execution.id, taskId ?? "");
  const agentRef = node?.agent;
  const agentVersion = useAgentVersion(
    orgId,
    projectId,
    agentRef?.agent_id ?? "",
    agentRef?.version ?? 0,
  );
  const task = detail.data;
  const lastAttempt = task?.attempts.at(-1);
  const metrics = task?.attempts.reduce(
    (sum, attempt) => ({
      tokens_in: sum.tokens_in + metric(attempt.metrics, "tokens_in"),
      tokens_out: sum.tokens_out + metric(attempt.metrics, "tokens_out"),
      duration_ms: sum.duration_ms + metric(attempt.metrics, "duration_ms"),
    }),
    { tokens_in: 0, tokens_out: 0, duration_ms: 0 },
  ) ?? { tokens_in: 0, tokens_out: 0, duration_ms: 0 };

  if (!node) {
    return (
      <EmptyState
        icon={Wrench}
        title="Select a node"
        description="Click a node in the graph to inspect its configuration, attempts and output."
      />
    );
  }
  if (!taskId) {
    return (
      <EmptyState
        icon={Clock3}
        title={`${node.id} has no task`}
        description="This node is inactive in the selected execution branch."
      />
    );
  }
  if (detail.isPending) {
    return <p className="text-sm text-muted-foreground">Loading node inspection…</p>;
  }
  if (!task) {
    return (
      <EmptyState
        icon={AlertTriangle}
        title="Task not found"
        description="The task row is unavailable."
      />
    );
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="font-mono text-sm">{node.id}</CardTitle>
            <CardDescription className="mt-1 flex items-center gap-2">
              <Badge variant="outline">{node.type}</Badge>
              <StatusIndicator status={task.state} />
            </CardDescription>
          </div>
          <span className="text-right text-[11px] text-muted-foreground">
            attempts {task.attempt_count}/{task.max_attempts}
            <br />
            {lastAttempt?.worker_id ?? "no worker"}
          </span>
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="grid grid-cols-2 gap-3 text-xs">
          <div>
            <p className="text-muted-foreground">Started</p>
            <p className="mt-0.5">{task.started_at ? formatDateTime(task.started_at) : "—"}</p>
          </div>
          <div>
            <p className="text-muted-foreground">Ended</p>
            <p className="mt-0.5">{task.ended_at ? formatDateTime(task.ended_at) : "—"}</p>
          </div>
          <div>
            <p className="text-muted-foreground">Duration</p>
            <p className="mt-0.5 font-mono">
              {formatDurationBetween(task.started_at, task.ended_at) === null
                ? "—"
                : formatDuration(formatDurationBetween(task.started_at, task.ended_at) ?? 0)}
            </p>
          </div>
          <div>
            <p className="text-muted-foreground">Checkpoint</p>
            <p className="mt-0.5">{lastAttempt?.checkpoint_available ? "available" : "none"}</p>
          </div>
        </div>

        {node.type === "agent" || node.type === "llm" ? (
          <section className="space-y-2">
            <h4 className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <Bot className="size-3.5" /> Model invocation
            </h4>
            <div className="grid grid-cols-2 gap-2 rounded-md border border-border p-3 text-xs">
              <div>
                <p className="text-muted-foreground">Provider</p>
                <p className="mt-0.5 font-mono">{agentVersion.data?.spec.model?.provider ?? "—"}</p>
              </div>
              <div>
                <p className="text-muted-foreground">Model</p>
                <p className="mt-0.5 font-mono">{agentVersion.data?.spec.model?.name ?? "—"}</p>
              </div>
              <div>
                <p className="text-muted-foreground">Tokens in</p>
                <p className="mt-0.5 font-mono">{metrics.tokens_in}</p>
              </div>
              <div>
                <p className="text-muted-foreground">Tokens out</p>
                <p className="mt-0.5 font-mono">{metrics.tokens_out}</p>
              </div>
              <div>
                <p className="text-muted-foreground">Latency</p>
                <p className="mt-0.5 font-mono">
                  {metrics.duration_ms > 0 ? formatDuration(metrics.duration_ms) : "—"}
                </p>
              </div>
              <div>
                <p className="text-muted-foreground">Est. cost</p>
                <p className="mt-0.5 font-mono">
                  {formatCost(metrics.tokens_in, metrics.tokens_out)}
                </p>
              </div>
            </div>
            <p className="text-[11px] text-muted-foreground">
              Request/response bodies are not persisted. Tool calls appear inside the attempt output
              when the agent reports them.
            </p>
          </section>
        ) : null}

        <section className="space-y-2">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            <Database className="size-3.5" /> Node configuration
          </h4>
          <CodeBlock code={redactedJson(node)} maxHeight="12rem" />
        </section>

        {Object.keys(nodeInputs).length > 0 ? (
          <section className="space-y-2">
            <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Node input (upstream outputs)
            </h4>
            {Object.entries(nodeInputs).map(([nodeId, output]) => (
              <div key={nodeId}>
                <p className="mb-1 font-mono text-[11px] text-muted-foreground">from {nodeId}</p>
                <CodeBlock code={redactedJson(output)} maxHeight="10rem" />
              </div>
            ))}
          </section>
        ) : null}

        <section className="space-y-2">
          <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Execution input (available to the graph)
          </h4>
          <CodeBlock code={redactedJson(executionInput ?? {})} maxHeight="10rem" />
        </section>

        <section className="space-y-2">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            <CheckCircle2 className="size-3.5" /> Output
          </h4>
          {task.result?.output ? (
            <CodeBlock code={redactedJson(task.result.output)} maxHeight="16rem" />
          ) : (
            <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
              No output for this task.
            </p>
          )}
        </section>

        {task.attempts.some((attempt) => attempt.error_code) ? (
          <section className="space-y-2">
            <h4 className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <AlertTriangle className="size-3.5 text-danger" /> Errors
            </h4>
            {task.attempts.map((attempt) => (
              <div
                key={attempt.id}
                className="rounded-md border border-danger/30 bg-danger/5 p-2 text-xs"
              >
                <p className="font-mono text-danger">{attempt.error_code ?? "unknown"}</p>
                <p className="mt-0.5 text-muted-foreground">
                  {attempt.error_message ?? "No message"}
                </p>
              </div>
            ))}
          </section>
        ) : null}

        <section className="space-y-2">
          <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Attempt history
          </h4>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>Outcome</TableHead>
                <TableHead>Worker</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>Error</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {task.attempts.map((attempt) => {
                const duration = formatDurationBetween(attempt.started_at, attempt.finished_at);
                return (
                  <TableRow key={attempt.id}>
                    <TableCell className="font-mono text-xs">{attempt.attempt_number}</TableCell>
                    <TableCell>
                      <StatusIndicator status={attempt.outcome ?? "running"} />
                    </TableCell>
                    <TableCell className="max-w-[8rem] truncate font-mono text-[10px]">
                      {attempt.worker_id}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {duration === null ? "—" : formatDuration(duration)}
                    </TableCell>
                    <TableCell className="text-xs text-danger">
                      {attempt.error_code ?? "—"}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </section>

        <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <Cpu className="size-3.5" /> Task {task.id} · execution {execution.id}
        </div>
      </CardContent>
    </Card>
  );
}
