"use client";

import { ExternalLink, GitBranch, Search } from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useWorkflowCatalog } from "@/lib/api/catalog";
import { useWorkspaceScope } from "@/lib/api/hooks";
import { formatDate, formatPercent, formatRelative } from "@/lib/format";
import { formatDuration } from "@/lib/utils";

export default function WorkflowsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const catalog = useWorkflowCatalog(orgId, projectId);
  const [query, setQuery] = useState("");
  const rows = useMemo(() => {
    const term = query.trim().toLowerCase();
    return (catalog.data ?? []).filter((row) =>
      !term
        ? true
        : row.workflow.name.toLowerCase().includes(term) ||
          row.workflow.id.toLowerCase().includes(term),
    );
  }, [catalog.data, query]);

  return (
    <>
      <PageHeader
        title="Workflows"
        description="Published graphs are immutable and every execution pins an exact version. Stats come from the latest 200 project executions."
        actions={
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-2 size-3.5 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Filter workflows"
                className="h-8 w-52 pl-8 text-xs"
                aria-label="Filter workflows"
              />
            </div>
            <Link href="/workflows/new" className={buttonVariants({ size: "sm" })}>
              New workflow
            </Link>
          </div>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Workflow definitions</CardTitle>
          <CardDescription>
            Version status, last execution, success rate and average duration.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {catalog.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading workflows…</p>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={GitBranch}
              title="No workflows"
              description="Create and publish a workflow through the SDK, CLI or API."
              className="m-4"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Workflow</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead className="hidden sm:table-cell">Last execution</TableHead>
                  <TableHead className="hidden md:table-cell">Success rate</TableHead>
                  <TableHead className="hidden lg:table-cell">Avg duration</TableHead>
                  <TableHead className="hidden xl:table-cell">Created</TableHead>
                  <TableHead className="w-20 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map(
                  ({ workflow, version, successRate, averageDurationMs, lastExecutionAt }) => (
                    <TableRow key={workflow.id}>
                      <TableCell className="max-w-[18rem]">
                        <Link
                          href={`/workflows/${workflow.id}`}
                          className="block truncate text-sm font-medium hover:underline"
                        >
                          {workflow.name}
                        </Link>
                        <p className="truncate font-mono text-[11px] text-muted-foreground">
                          {workflow.id}
                        </p>
                      </TableCell>
                      <TableCell>
                        <Badge variant="neutral" className="font-mono text-[10px]">
                          v{workflow.latest_version}
                        </Badge>
                        <span className="ml-2">
                          <StatusIndicator status={version?.status ?? "draft"} />
                        </span>
                      </TableCell>
                      <TableCell className="hidden text-xs text-muted-foreground sm:table-cell">
                        {formatRelative(lastExecutionAt)}
                      </TableCell>
                      <TableCell className="hidden text-sm tabular-nums text-muted-foreground md:table-cell">
                        {formatPercent(successRate)}
                      </TableCell>
                      <TableCell className="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                        {averageDurationMs === null ? "—" : formatDuration(averageDurationMs)}
                      </TableCell>
                      <TableCell className="hidden text-xs text-muted-foreground xl:table-cell">
                        {formatDate(workflow.created_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        <Link
                          href={`/workflows/${workflow.id}`}
                          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                        >
                          Inspect <ExternalLink className="size-3" aria-hidden="true" />
                        </Link>
                      </TableCell>
                    </TableRow>
                  ),
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </>
  );
}
