"use client";

import { ArrowUpRight, Braces } from "lucide-react";
import Link from "next/link";
import { buttonVariants } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code-block";
import { Drawer } from "@/components/ui/drawer";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { demoEvents, findDemoExecution } from "@/lib/demo-data";
import { formatDateTime, formatDuration } from "@/lib/utils";
import { useExecutionDrawer } from "@/stores/ui-store";

/** One drawer instance for the whole app, driven by the Zustand selection. */
export function ExecutionDrawer() {
  const selectedId = useExecutionDrawer((state) => state.selectedId);
  const close = useExecutionDrawer((state) => state.close);
  const execution = selectedId ? findDemoExecution(selectedId) : undefined;

  return (
    <Drawer
      open={Boolean(execution)}
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={execution ? execution.workflowName : "Execution"}
      description={execution ? execution.id : undefined}
      footer={
        execution ? (
          <Link
            href={`/executions/${execution.id}`}
            onClick={close}
            className={buttonVariants({ variant: "outline", size: "sm" })}
          >
            Open full view
            <ArrowUpRight className="size-3.5" aria-hidden="true" />
          </Link>
        ) : null
      }
    >
      {execution ? (
        <div className="space-y-5">
          <div className="flex items-center justify-between">
            <StatusIndicator status={execution.status} />
            <span className="text-xs text-muted-foreground">
              v{execution.workflowVersion} · {formatDuration(execution.durationMs)}
            </span>
          </div>

          <dl className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Started</dt>
              <dd className="mt-0.5">{formatDateTime(execution.startedAt)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Triggered by</dt>
              <dd className="mt-0.5">{execution.triggeredBy}</dd>
            </div>
          </dl>

          <section className="space-y-2">
            <h3 className="flex items-center gap-1.5 text-sm font-medium">
              <Braces className="size-3.5 text-muted-foreground" aria-hidden="true" />
              Input
            </h3>
            <CodeBlock code={JSON.stringify(execution.input, null, 2)} maxHeight="12rem" />
          </section>

          <section className="space-y-2">
            <h3 className="text-sm font-medium">Recent events</h3>
            <ol className="space-y-2 border-l border-border pl-4">
              {demoEvents.slice(0, 4).map((event) => (
                <li key={event.id} className="text-xs">
                  <p className="font-mono text-foreground">{event.type}</p>
                  <p className="mt-0.5 text-muted-foreground">
                    {event.detail} · {event.actor}
                  </p>
                </li>
              ))}
            </ol>
          </section>
        </div>
      ) : null}
    </Drawer>
  );
}
