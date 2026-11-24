"use client";

import { ExternalLink } from "lucide-react";
import Link from "next/link";

import { Badge } from "@/components/ui/badge";
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
import type { Execution } from "@/lib/api/types";
import { formatDurationBetween } from "@/lib/format";
import { formatDateTime, formatDuration } from "@/lib/utils";
import { useExecutionDrawer } from "@/stores/ui-store";

export function ExecutionsTable({
  executions,
  limit,
  workflowNames = {},
  isLoading = false,
  emptyMessage = "No executions match this view.",
}: {
  executions: Execution[];
  limit?: number;
  workflowNames?: Record<string, string>;
  isLoading?: boolean;
  emptyMessage?: string;
}) {
  const openDrawer = useExecutionDrawer((state) => state.open);
  const rows = limit ? executions.slice(0, limit) : executions;

  if (isLoading) {
    return <p className="px-5 py-8 text-sm text-muted-foreground">Loading executions…</p>;
  }

  if (rows.length === 0) {
    return <EmptyState title="No executions" description={emptyMessage} className="m-4" />;
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Execution</TableHead>
          <TableHead>Workflow</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="hidden lg:table-cell">Progress</TableHead>
          <TableHead className="hidden md:table-cell">Started</TableHead>
          <TableHead className="hidden sm:table-cell">Duration</TableHead>
          <TableHead className="w-20 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((execution) => {
          const duration = formatDurationBetween(execution.started_at, execution.ended_at);
          const total = Math.max(1, execution.counts.total);
          return (
            <TableRow key={execution.id}>
              <TableCell className="max-w-[15rem]">
                <button
                  type="button"
                  onClick={() => openDrawer(execution.id)}
                  className="block max-w-full truncate font-mono text-xs text-primary hover:underline"
                  title={execution.id}
                >
                  {execution.id}
                </button>
              </TableCell>
              <TableCell className="max-w-[14rem]">
                <Link
                  href={`/workflows/${execution.workflow_id}`}
                  className="block truncate text-sm hover:underline"
                  title={execution.workflow_id}
                >
                  {workflowNames[execution.workflow_id] ?? execution.workflow_id}
                </Link>
                <Badge variant="neutral" className="mt-0.5 font-mono text-[10px]">
                  v{execution.workflow_version}
                </Badge>
              </TableCell>
              <TableCell>
                <StatusIndicator status={execution.state} />
              </TableCell>
              <TableCell className="hidden lg:table-cell">
                <div className="flex items-center gap-2">
                  <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-success/70"
                      style={{
                        width: `${Math.round((execution.counts.succeeded / total) * 100)}%`,
                      }}
                    />
                  </div>
                  <span className="font-mono text-[11px] text-muted-foreground">
                    {execution.counts.succeeded}/{execution.counts.total}
                  </span>
                </div>
              </TableCell>
              <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground md:table-cell">
                {execution.created_at ? formatDateTime(execution.created_at) : "—"}
              </TableCell>
              <TableCell className="hidden font-mono text-xs text-muted-foreground sm:table-cell">
                {duration === null ? "—" : formatDuration(duration)}
              </TableCell>
              <TableCell className="text-right">
                <Link
                  href={`/executions/${execution.id}`}
                  className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                >
                  Open <ExternalLink className="size-3" aria-hidden="true" />
                </Link>
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}
