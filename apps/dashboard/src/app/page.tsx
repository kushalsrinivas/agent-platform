"use client";

import { Activity, CircleX, Coins, Cpu, ListTodo, Percent, Timer } from "lucide-react";
import { useMemo, useState } from "react";

import { defaultRange, TimeRangePicker } from "@/components/dashboard/time-range";
import { ExecutionsTable } from "@/components/executions/executions-table";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  useExecutions,
  useUsage,
  useWorkers,
  useWorkflows,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import { formatCost, formatNumber, formatPercent } from "@/lib/format";
import { formatDuration } from "@/lib/utils";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

export default function OverviewPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const [range, setRange] = useState(() => defaultRange("24h"));
  const usage = useUsage(orgId, projectId, range.from, range.to);
  const running = useExecutions(orgId, projectId, { state: "running", limit: 50 });
  const pending = useExecutions(orgId, projectId, { state: "pending", limit: 50 });
  const recent = useExecutions(orgId, projectId, {
    createdAfter: range.from,
    createdBefore: range.to,
    limit: 100,
  });
  const workers = useWorkers(orgId);
  const workflows = useWorkflows(orgId, projectId);
  const workflowNames = useMemo(
    () =>
      Object.fromEntries(
        (workflows.data?.items ?? []).map((workflow) => [workflow.id, workflow.name]),
      ),
    [workflows.data],
  );

  const total = usage.data?.total;
  const terminalCount = (total?.succeeded ?? 0) + (total?.failed ?? 0);
  const successRate = terminalCount > 0 ? ((total?.succeeded ?? 0) / terminalCount) * 100 : null;
  const averageDuration =
    total && total.executions > 0 ? total.duration_ms / total.executions : null;
  const active = (running.data?.items.length ?? 0) + (pending.data?.items.length ?? 0);
  const workerList = workers.data?.items ?? [];
  const available = workerList.reduce((sum, worker) => sum + worker.available_slots, 0);
  const capacity = workerList.reduce((sum, worker) => sum + worker.max_concurrency, 0);
  const recentItems = recent.data?.items ?? [];
  const counts = {
    active: recentItems.filter((execution) => !TERMINAL.has(execution.state)).length,
    succeeded: recentItems.filter((execution) => execution.state === "succeeded").length,
    failed: recentItems.filter((execution) => execution.state === "failed").length,
    cancelled: recentItems.filter((execution) => execution.state === "cancelled").length,
  };

  if (!orgId || !projectId) {
    return (
      <EmptyState
        title="Choose an organization and project"
        description="The dashboard reads only the selected project. Use the selectors above to begin."
      />
    );
  }

  return (
    <>
      <PageHeader
        title="Overview"
        description="Execution health, capacity and spend for the selected project. Active and queued counts are live; the remaining metrics use the selected range."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{active} active</Badge>
            <TimeRangePicker value={range} onChange={setRange} />
          </div>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric
          label="Total executions"
          value={formatNumber(total?.executions ?? 0)}
          hint={range.key === "custom" ? "custom range" : `last ${range.key}`}
          icon={Activity}
        />
        <Metric
          label="Active now"
          value={formatNumber(active)}
          hint="running + queued"
          icon={Activity}
        />
        <Metric
          label="Queued"
          value={formatNumber(pending.data?.items.length ?? 0)}
          hint="waiting for a worker"
          icon={ListTodo}
        />
        <Metric
          label="Failed"
          value={formatNumber(total?.failed ?? 0)}
          hint="in selected range"
          icon={CircleX}
          tone="text-danger"
        />
        <Metric
          label="Success rate"
          value={formatPercent(successRate)}
          hint="terminal executions"
          icon={Percent}
          tone="text-success"
        />
        <Metric
          label="Average duration"
          value={averageDuration === null ? "—" : formatDuration(Math.round(averageDuration))}
          hint="attempt time / execution"
          icon={Timer}
        />
        <Metric
          label="Tokens"
          value={formatNumber((total?.tokens_in ?? 0) + (total?.tokens_out ?? 0))}
          hint={`${formatNumber(total?.tokens_in ?? 0)} in / ${formatNumber(total?.tokens_out ?? 0)} out`}
          icon={Cpu}
        />
        <Metric
          label="Est. model cost"
          value={formatCost(total?.tokens_in ?? 0, total?.tokens_out ?? 0)}
          hint="at $0.15 / $0.60 per 1M tokens"
          icon={Coins}
        />
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Recent executions</CardTitle>
              <CardDescription>
                Newest first. Open a row to inspect the full graph and failure.
              </CardDescription>
            </div>
            <Badge variant="outline">{(recent.data?.items ?? []).length} in range</Badge>
          </CardHeader>
          <CardContent className="px-2 pb-2">
            <ExecutionsTable
              executions={recent.data?.items ?? []}
              workflowNames={workflowNames}
              isLoading={recent.isPending}
              limit={8}
            />
          </CardContent>
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle>Worker availability</CardTitle>
              <CardDescription>Free slots across the selected organization.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-end justify-between">
                <p className="text-2xl font-semibold tabular-nums">
                  {available}
                  <span className="text-sm font-normal text-muted-foreground">/{capacity}</span>
                </p>
                <Badge variant={available > 0 ? "success" : "danger"}>
                  {available > 0 ? "accepting work" : "saturated"}
                </Badge>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-success/70"
                  style={{
                    width: `${capacity > 0 ? Math.round((available / capacity) * 100) : 0}%`,
                  }}
                />
              </div>
              <ul className="space-y-2">
                {workerList.slice(0, 4).map((worker) => (
                  <li key={worker.id} className="flex items-center justify-between gap-2 text-xs">
                    <span className="truncate font-mono">{worker.pool}</span>
                    <span className="flex items-center gap-2 text-muted-foreground">
                      {worker.available_slots}/{worker.max_concurrency}
                      <StatusIndicator status={worker.status} showLabel={false} />
                    </span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Execution mix</CardTitle>
              <CardDescription>Recent runs in the selected range.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {(
                [
                  ["active", "Active", counts.active],
                  ["succeeded", "Succeeded", counts.succeeded],
                  ["failed", "Failed", counts.failed],
                  ["cancelled", "Cancelled", counts.cancelled],
                ] as const
              ).map(([key, _label, value]) => {
                const width = `${Math.round((value / Math.max(1, recent.data?.items.length ?? 1)) * 100)}%`;
                return (
                  <div key={key} className="space-y-1.5">
                    <div className="flex items-center justify-between">
                      <StatusIndicator status={key} />
                      <span className="text-xs tabular-nums text-muted-foreground">{value}</span>
                    </div>
                    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                      <div className="h-full rounded-full bg-primary/60" style={{ width }} />
                    </div>
                  </div>
                );
              })}
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}

function Metric({
  label,
  value,
  hint,
  icon: Icon,
  tone = "text-muted-foreground",
}: {
  label: string;
  value: string;
  hint: string;
  icon: typeof Activity;
  tone?: string;
}) {
  return (
    <Card>
      <CardContent className="flex items-start justify-between p-4">
        <div className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {label}
          </p>
          <p className="mt-1.5 truncate text-2xl font-semibold tabular-nums">{value}</p>
          <p className="mt-1 truncate text-[11px] text-muted-foreground">{hint}</p>
        </div>
        <Icon className={`size-4 shrink-0 ${tone}`} aria-hidden="true" />
      </CardContent>
    </Card>
  );
}
