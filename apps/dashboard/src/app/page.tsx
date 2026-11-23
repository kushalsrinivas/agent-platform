"use client";

import { Activity, CircleCheck, CircleX, Timer } from "lucide-react";

import { ExecutionsTable } from "@/components/executions/executions-table";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { demoEvents, demoExecutions } from "@/lib/demo-data";
import type { ExecutionStatus } from "@/lib/schemas";
import { formatDateTime } from "@/lib/utils";

const STATS = [
  { key: "running", label: "Running now", icon: Activity, tone: "text-info" },
  { key: "succeeded", label: "Succeeded today", icon: CircleCheck, tone: "text-success" },
  { key: "failed", label: "Failed today", icon: CircleX, tone: "text-danger" },
] as const;

const MIX: ExecutionStatus[] = ["running", "queued", "succeeded", "failed", "cancelled"];

export default function OverviewPage() {
  const total = demoExecutions.length;
  const successRate = Math.round(
    (demoExecutions.filter((execution) => execution.status === "succeeded").length /
      Math.max(total, 1)) *
      100,
  );
  const p50 = [...demoExecutions]
    .filter((execution) => execution.durationMs > 0)
    .sort((a, b) => a.durationMs - b.durationMs)
    .at(Math.floor(total / 2));
  const counts = Object.fromEntries(
    MIX.map((status) => [status, demoExecutions.filter((e) => e.status === status).length]),
  ) as Record<ExecutionStatus, number>;

  return (
    <>
      <PageHeader
        title="Overview"
        description="Execution health, recent runs and the status of the local stack. Values on this page come from fixtures until the API is wired."
        actions={<Badge variant="outline">Phase 02 shell</Badge>}
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {STATS.map((stat) => {
          const Icon = stat.icon;
          const value = demoExecutions.filter((e) => e.status === stat.key).length;
          return (
            <Card key={stat.key}>
              <CardContent className="flex items-start justify-between p-5">
                <div>
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {stat.label}
                  </p>
                  <p className="mt-2 text-2xl font-semibold tabular-nums">{value}</p>
                </div>
                <Icon className={`size-4 ${stat.tone}`} aria-hidden="true" />
              </CardContent>
            </Card>
          );
        })}
        <Card>
          <CardContent className="flex items-start justify-between p-5">
            <div>
              <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Success rate
              </p>
              <p className="mt-2 text-2xl font-semibold tabular-nums">{successRate}%</p>
            </div>
            <Timer className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardContent>
        </Card>
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Recent executions</CardTitle>
              <CardDescription>
                Newest first. Select a row to inspect its input and events.
              </CardDescription>
            </div>
            <Badge variant="outline">
              p50 {p50 ? `${(p50.durationMs / 1000).toFixed(1)}s` : "—"}
            </Badge>
          </CardHeader>
          <CardContent className="px-2 pb-2">
            <ExecutionsTable limit={5} />
          </CardContent>
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle>Execution mix</CardTitle>
              <CardDescription>All demo executions by terminal and active state.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {MIX.map((status) => {
                const count = counts[status];
                const width = `${Math.round((count / Math.max(total, 1)) * 100)}%`;
                return (
                  <div key={status} className="space-y-1.5">
                    <div className="flex items-center justify-between">
                      <StatusIndicator status={status} />
                      <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
                    </div>
                    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-primary/60"
                        style={{ width }}
                        aria-hidden="true"
                      />
                    </div>
                  </div>
                );
              })}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Latest activity</CardTitle>
              <CardDescription>Append-only execution events (fixture).</CardDescription>
            </CardHeader>
            <CardContent>
              <ol className="space-y-3 border-l border-border pl-4">
                {demoEvents.slice(0, 4).map((event) => (
                  <li key={event.id} className="text-xs">
                    <p className="font-mono text-foreground">{event.type}</p>
                    <p className="mt-0.5 text-muted-foreground">{event.detail}</p>
                    <p className="mt-0.5 text-[11px] text-muted-foreground/80">
                      {formatDateTime(event.at)} · {event.actor}
                    </p>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}
