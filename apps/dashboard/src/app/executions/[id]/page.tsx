"use client";

import { ArrowLeft, Ban } from "lucide-react";
import Link from "next/link";
import { use } from "react";

import { WorkflowGraph } from "@/components/graph/workflow-graph";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { demoEvents, findDemoExecution } from "@/lib/demo-data";
import { cn, formatDateTime, formatDuration } from "@/lib/utils";

export default function ExecutionDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const execution = findDemoExecution(id);

  if (!execution) {
    return (
      <div className="mx-auto max-w-lg py-16 text-center">
        <h1 className="text-lg font-semibold">Execution not found</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Unknown execution ID. The demo dataset only contains a handful of runs.
        </p>
        <Link href="/executions" className={cn(buttonVariants({ variant: "outline" }), "mt-5")}>
          <ArrowLeft className="size-4" aria-hidden="true" />
          Back to executions
        </Link>
      </div>
    );
  }

  return (
    <>
      <div className="mb-4">
        <Link
          href="/executions"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          Executions
        </Link>
      </div>

      <PageHeader
        title={execution.workflowName}
        description={`${execution.id} · workflow v${execution.workflowVersion} · triggered by ${execution.triggeredBy}`}
        actions={
          <>
            <StatusIndicator status={execution.status} />
            <Button variant="outline" size="sm" disabled title="Wired in Phase 04">
              <Ban className="size-3.5" aria-hidden="true" />
              Cancel
            </Button>
          </>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <Card>
          <CardContent className="p-5">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Started</p>
            <p className="mt-1.5 text-sm">{formatDateTime(execution.startedAt)}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Duration</p>
            <p className="mt-1.5 font-mono text-sm">{formatDuration(execution.durationMs)}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">
              Workflow version
            </p>
            <p className="mt-1.5 text-sm">
              v{execution.workflowVersion} <Badge variant="neutral">pinned</Badge>
            </p>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Graph</CardTitle>
          <CardDescription>
            Node state at the last committed event. Edges evaluate CEL conditions; only activated
            branches consume the join.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <WorkflowGraph className="h-[400px] w-full" />
        </CardContent>
      </Card>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Input</CardTitle>
            <CardDescription>Immutable, capped at 256 KiB, stored with a digest.</CardDescription>
          </CardHeader>
          <CardContent>
            <CodeBlock code={JSON.stringify(execution.input, null, 2)} label="input.json" />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Output</CardTitle>
            <CardDescription>
              Inline result up to 64 KiB; larger data is referenced as an artifact.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {execution.output ? (
              <CodeBlock code={JSON.stringify(execution.output, null, 2)} label="output.json" />
            ) : (
              <p className="rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-muted-foreground">
                No output committed yet.
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Event timeline</CardTitle>
          <CardDescription>
            Audit trail. The transition and its event commit in the same transaction.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ol className="space-y-4 border-l border-border pl-5">
            {demoEvents.map((event) => (
              <li key={event.id} className="relative">
                <span className="absolute -left-[1.45rem] top-1.5 size-2 rounded-full border-2 border-background bg-primary" />
                <p className="font-mono text-xs">{event.type}</p>
                <p className="mt-0.5 text-sm text-muted-foreground">{event.detail}</p>
                <p className="mt-0.5 text-[11px] text-muted-foreground/80">
                  {formatDateTime(event.at)} · {event.actor} · {event.id}
                </p>
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>
    </>
  );
}
