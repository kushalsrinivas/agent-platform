"use client";

import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { getDemoExecutions } from "@/lib/demo-data";
import type { Execution } from "@/lib/schemas";
import { formatDateTime, formatDuration } from "@/lib/utils";
import { useExecutionDrawer } from "@/stores/ui-store";

export function ExecutionsTable({ limit }: { limit?: number }) {
  const openDrawer = useExecutionDrawer((state) => state.open);
  const {
    data = [],
    isPending,
    isError,
    refetch,
  } = useQuery({
    queryKey: ["executions"],
    queryFn: getDemoExecutions,
  });

  const rows = limit ? data.slice(0, limit) : data;

  if (isPending) {
    return <p className="px-5 py-8 text-sm text-muted-foreground">Loading executions…</p>;
  }

  if (isError) {
    return (
      <div className="px-5 py-8">
        <p className="text-sm text-danger">Could not load executions.</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Execution</TableHead>
          <TableHead>Workflow</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="hidden md:table-cell">Started</TableHead>
          <TableHead className="hidden sm:table-cell">Duration</TableHead>
          <TableHead className="w-16 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((execution: Execution) => (
          <TableRow key={execution.id}>
            <TableCell className="max-w-[16rem]">
              <button
                type="button"
                onClick={() => openDrawer(execution.id)}
                className="block truncate font-mono text-xs text-primary hover:underline"
                title={execution.id}
              >
                {execution.id}
              </button>
            </TableCell>
            <TableCell>
              <span className="text-sm">{execution.workflowName}</span>
              <Badge variant="neutral" className="ml-2 font-mono text-[10px]">
                v{execution.workflowVersion}
              </Badge>
            </TableCell>
            <TableCell>
              <StatusIndicator status={execution.status} />
            </TableCell>
            <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground md:table-cell">
              {formatDateTime(execution.startedAt)}
            </TableCell>
            <TableCell className="hidden font-mono text-xs text-muted-foreground sm:table-cell">
              {formatDuration(execution.durationMs)}
            </TableCell>
            <TableCell className="text-right">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => openDrawer(execution.id)}
                aria-label={`View execution ${execution.id}`}
              >
                View
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
