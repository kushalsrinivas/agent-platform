"use client";

import { useState } from "react";

import { defaultRange, TimeRangePicker } from "@/components/dashboard/time-range";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useUsage, useWorkspaceScope } from "@/lib/api/hooks";
import { formatCost, formatNumber, formatPercent } from "@/lib/format";
import { formatDuration } from "@/lib/utils";

export default function UsagePage() {
  const { orgId, projectId } = useWorkspaceScope();
  const [range, setRange] = useState(() => defaultRange("30d"));
  const usage = useUsage(orgId, projectId, range.from, range.to);
  const total = usage.data?.total;
  const projects = usage.data?.projects ?? [];
  const terminal = (total?.succeeded ?? 0) + (total?.failed ?? 0);

  return (
    <>
      <PageHeader
        title="Usage"
        description="Attempt-level tokens and duration aggregated per project. Cost is estimated from token counts and the pricing shown below; it is not an invoice."
        actions={<TimeRangePicker value={range} onChange={setRange} />}
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="Executions" value={formatNumber(total?.executions ?? 0)} />
        <Stat
          label="Success rate"
          value={formatPercent(terminal ? ((total?.succeeded ?? 0) / terminal) * 100 : null)}
        />
        <Stat
          label="Tokens"
          value={formatNumber((total?.tokens_in ?? 0) + (total?.tokens_out ?? 0))}
          hint={`${formatNumber(total?.tokens_in ?? 0)} in / ${formatNumber(total?.tokens_out ?? 0)} out`}
        />
        <Stat
          label="Est. cost"
          value={formatCost(total?.tokens_in ?? 0, total?.tokens_out ?? 0)}
          hint="$0.15 / $0.60 per 1M tokens"
        />
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Per-project usage</CardTitle>
          <CardDescription>
            Attempt duration is summed, so a retried task contributes once per attempt.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {usage.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading usage…</p>
          ) : projects.length === 0 ? (
            <EmptyState
              title="No usage in this range"
              description="Run an execution or widen the time range."
              className="m-4"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Project</TableHead>
                  <TableHead>Executions</TableHead>
                  <TableHead className="hidden md:table-cell">Attempts</TableHead>
                  <TableHead className="hidden sm:table-cell">Tokens in</TableHead>
                  <TableHead className="hidden sm:table-cell">Tokens out</TableHead>
                  <TableHead>Duration</TableHead>
                  <TableHead className="text-right">Est. cost</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {projects.map((project) => (
                  <TableRow key={project.project_id}>
                    <TableCell className="max-w-[16rem]">
                      <p className="truncate font-mono text-xs">{project.project_id}</p>
                      <p className="text-[11px] text-muted-foreground">
                        {project.succeeded} ok · {project.failed} failed
                      </p>
                    </TableCell>
                    <TableCell className="text-sm tabular-nums">
                      {formatNumber(project.executions)}
                    </TableCell>
                    <TableCell className="hidden text-sm tabular-nums text-muted-foreground md:table-cell">
                      {formatNumber(project.attempts)}
                    </TableCell>
                    <TableCell className="hidden text-sm tabular-nums sm:table-cell">
                      {formatNumber(project.tokens_in)}
                    </TableCell>
                    <TableCell className="hidden text-sm tabular-nums sm:table-cell">
                      {formatNumber(project.tokens_out)}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatDuration(project.duration_ms)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatCost(project.tokens_in, project.tokens_out)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="mt-1.5 text-xl font-semibold tabular-nums">{value}</p>
        {hint ? <p className="mt-1 truncate text-[11px] text-muted-foreground">{hint}</p> : null}
      </CardContent>
    </Card>
  );
}
