"use client";

import { ArrowUpRight, Braces } from "lucide-react";
import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code-block";
import { Drawer } from "@/components/ui/drawer";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  useExecution,
  useExecutionEvents,
  useExecutionInput,
  useExecutionTasks,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import { formatDurationBetween } from "@/lib/format";
import { redactedJson } from "@/lib/redact";
import { formatDateTime, formatDuration } from "@/lib/utils";
import { useExecutionDrawer } from "@/stores/ui-store";

export function ExecutionDrawer() {
  const selectedId = useExecutionDrawer((state) => state.selectedId);
  const close = useExecutionDrawer((state) => state.close);
  const { orgId, projectId } = useWorkspaceScope();
  const execution = useExecution(orgId, projectId, selectedId ?? "");
  const tasks = useExecutionTasks(orgId, projectId, selectedId ?? "");
  const events = useExecutionEvents(orgId, projectId, selectedId ?? "");
  const input = useExecutionInput(orgId, projectId, selectedId ?? "");
  const value = execution.data;
  const duration = value ? formatDurationBetween(value.started_at, value.ended_at) : null;

  return (
    <Drawer
      open={Boolean(selectedId)}
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={value ? `Execution ${value.id.slice(0, 13)}…` : "Execution"}
      description={
        value ? `${value.state} · ${value.counts.succeeded}/${value.counts.total} nodes` : undefined
      }
      footer={
        value ? (
          <Link
            href={`/executions/${value.id}`}
            onClick={close}
            className={buttonVariants({ variant: "outline", size: "sm" })}
          >
            Open explorer <ArrowUpRight className="size-3.5" aria-hidden="true" />
          </Link>
        ) : null
      }
    >
      {value ? (
        <div className="space-y-5">
          <div className="flex items-center justify-between">
            <StatusIndicator status={value.state} />
            <span className="text-xs text-muted-foreground">
              v{value.workflow_version}
              {duration === null ? "" : ` · ${formatDuration(duration)}`}
            </span>
          </div>

          <dl className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Created</dt>
              <dd className="mt-0.5 text-xs">{formatDateTime(value.created_at)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Triggered by</dt>
              <dd className="mt-0.5 truncate text-xs">{value.created_by}</dd>
            </div>
          </dl>

          <section className="space-y-2">
            <h3 className="text-sm font-medium">Nodes</h3>
            <ul className="space-y-1.5">
              {(tasks.data?.items ?? []).slice(0, 8).map((task) => (
                <li key={task.id} className="flex items-center justify-between gap-2 text-xs">
                  <span className="min-w-0 truncate font-mono">{task.node_id}</span>
                  <StatusIndicator status={task.state} />
                </li>
              ))}
            </ul>
          </section>

          <section className="space-y-2">
            <h3 className="flex items-center gap-1.5 text-sm font-medium">
              <Braces className="size-3.5 text-muted-foreground" aria-hidden="true" />
              Input
            </h3>
            <CodeBlock code={redactedJson(input.data?.input ?? {})} maxHeight="10rem" />
          </section>

          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-medium">Latest events</h3>
              <Badge variant="outline">{(events.data ?? []).length}</Badge>
            </div>
            <ol className="space-y-2 border-l border-border pl-4">
              {(events.data ?? [])
                .slice(-6)
                .reverse()
                .map((event) => (
                  <li key={event.seq} className="text-xs">
                    <p className="font-mono text-foreground">{event.event_type}</p>
                    <p className="mt-0.5 text-muted-foreground">
                      {event.to_state ?? event.entity_id}
                      {event.reason ? ` · ${event.reason}` : ""}
                    </p>
                  </li>
                ))}
            </ol>
          </section>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">Loading execution…</p>
      )}
    </Drawer>
  );
}
