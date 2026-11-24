"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Ban, ExternalLink, Radio } from "lucide-react";
import Link from "next/link";

import { EventTimeline } from "@/components/executions/event-timeline";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code-block";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { toast } from "@/components/ui/toast";
import { api, apiPaths } from "@/lib/api";
import {
  useExecution,
  useExecutionEvents,
  useExecutionInput,
  useExecutionTasks,
  useTaskDetail,
} from "@/lib/api/hooks";
import { useExecutionStream } from "@/lib/api/stream";
import { redactedJson } from "@/lib/redact";
import { cn, formatDateTime, formatDuration } from "@/lib/utils";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

export function ExecutionPanel({
  orgId,
  projectId,
  executionId,
  selectedNodeId,
  onClear,
}: {
  orgId: string;
  projectId: string;
  executionId: string;
  selectedNodeId?: string;
  onClear: () => void;
}) {
  const queryClient = useQueryClient();
  const execution = useExecution(orgId, projectId, executionId);
  const tasks = useExecutionTasks(orgId, projectId, executionId);
  const events = useExecutionEvents(orgId, projectId, executionId);
  const input = useExecutionInput(orgId, projectId, executionId);
  const selectedTask = (tasks.data?.items ?? []).find((task) => task.node_id === selectedNodeId);
  const detail = useTaskDetail(orgId, projectId, executionId, selectedTask?.id ?? "");
  const live = useExecutionStream(
    orgId,
    projectId,
    executionId,
    Boolean(execution.data && !TERMINAL.has(execution.data.state)),
  );

  async function cancel() {
    try {
      await api(`${apiPaths.execution(orgId, projectId, executionId)}:cancel`, {
        method: "POST",
        json: { reason: "Cancelled from builder" },
      });
      await queryClient.invalidateQueries({
        queryKey: ["execution", orgId, projectId, executionId],
      });
      toast.success("Cancellation requested");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not cancel execution");
    }
  }

  if (execution.isError) {
    return (
      <div className="space-y-2 p-4 text-xs text-muted-foreground">
        <p>Could not load this execution.</p>
        <Button size="sm" variant="outline" onClick={onClear}>
          Clear
        </Button>
      </div>
    );
  }

  const value = execution.data;
  return (
    <div className="space-y-4 p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-sm font-medium">Live execution</p>
          <p className="truncate font-mono text-[11px] text-muted-foreground">{executionId}</p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {live.state === "live" ? (
            <Badge variant="success" className="gap-1">
              <Radio className="size-3" /> live
            </Badge>
          ) : live.state === "reconnecting" ? (
            <Badge variant="warning">reconnecting</Badge>
          ) : null}
          <Button variant="ghost" size="sm" onClick={onClear}>
            Clear
          </Button>
        </div>
      </div>

      {!value ? (
        <p className="text-xs text-muted-foreground">Loading execution…</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <StatusIndicator status={value.state} />
            <Badge variant="outline">v{value.workflow_version}</Badge>
            <Badge variant="outline">
              {value.counts.succeeded}/{value.counts.total} nodes
            </Badge>
            {!TERMINAL.has(value.state) ? (
              <Button size="sm" variant="outline" onClick={cancel}>
                <Ban className="size-3.5" /> Cancel
              </Button>
            ) : null}
            <Link
              href={`/executions/${value.id}`}
              className={cn(buttonVariants({ variant: "ghost", size: "sm" }), "gap-1")}
            >
              Explorer <ExternalLink className="size-3" />
            </Link>
          </div>
          {value.failure_code ? (
            <p className="rounded-md border border-danger/30 bg-danger/5 p-2 text-xs text-danger">
              {value.failure_code}
              {value.failure_detail ? ` · ${value.failure_detail}` : ""}
            </p>
          ) : null}
        </>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-medium">Node execution</h3>
          <span className="font-mono text-[10px] text-muted-foreground">
            {selectedNodeId ?? "none selected"}
          </span>
        </div>
        {!selectedTask ? (
          <p className="rounded-md border border-dashed border-border p-2 text-[11px] text-muted-foreground">
            Click a node on the canvas to inspect its attempt, output and errors.
          </p>
        ) : detail.isPending ? (
          <p className="text-xs text-muted-foreground">Loading node…</p>
        ) : !detail.data ? (
          <p className="text-xs text-muted-foreground">Task detail is unavailable.</p>
        ) : (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <StatusIndicator status={detail.data.state} />
              <Badge variant="outline">
                attempts {detail.data.attempt_count}/{detail.data.max_attempts}
              </Badge>
              {detail.data.started_at && detail.data.ended_at ? (
                <Badge variant="neutral">
                  {formatDuration(
                    new Date(detail.data.ended_at).getTime() -
                      new Date(detail.data.started_at).getTime(),
                  )}
                </Badge>
              ) : null}
            </div>
            {detail.data.attempts.map((attempt) => (
              <div
                key={attempt.id}
                className={cn(
                  "rounded-md border p-2 text-[11px]",
                  attempt.error_code ? "border-danger/30 bg-danger/5" : "border-border",
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono">attempt {attempt.attempt_number}</span>
                  <StatusIndicator status={attempt.outcome ?? "running"} />
                </div>
                <p className="mt-1 text-muted-foreground">
                  {attempt.worker_id} · {formatDateTime(attempt.dispatched_at)}
                </p>
                {attempt.metrics && Object.keys(attempt.metrics).length > 0 ? (
                  <p className="mt-1 font-mono text-[10px] text-muted-foreground">
                    {Object.entries(attempt.metrics)
                      .map(([key, metric]) => `${key}:${String(metric)}`)
                      .join(" · ")}
                  </p>
                ) : null}
                {attempt.error_code ? (
                  <p className="mt-1 text-danger">
                    {attempt.error_code}
                    {attempt.error_message ? ` · ${attempt.error_message}` : ""}
                  </p>
                ) : null}
                {attempt.checkpoint_available ? (
                  <p className="mt-1 text-[10px] text-muted-foreground">checkpoint available</p>
                ) : null}
              </div>
            ))}
            {detail.data.result?.output ? (
              <CodeBlock
                code={redactedJson(detail.data.result.output)}
                label="output.json"
                maxHeight="12rem"
              />
            ) : null}
          </div>
        )}
      </section>

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-medium">Execution input</h3>
        </div>
        <CodeBlock code={redactedJson(input.data?.input ?? {})} maxHeight="8rem" />
      </section>

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-medium">Live log</h3>
          <Badge variant="outline">{events.data?.length ?? 0}</Badge>
        </div>
        <div className="max-h-80 overflow-y-auto">
          <EventTimeline events={(events.data ?? []).slice(-40)} dense />
        </div>
      </section>
    </div>
  );
}
