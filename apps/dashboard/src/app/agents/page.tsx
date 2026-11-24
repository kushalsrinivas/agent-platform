"use client";

import { Bot, ExternalLink, Search } from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
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
import { useAgentCatalog } from "@/lib/api/catalog";
import { useWorkspaceScope } from "@/lib/api/hooks";
import { formatRelative } from "@/lib/format";

export default function AgentsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const catalog = useAgentCatalog(orgId, projectId);
  const [query, setQuery] = useState("");
  const rows = useMemo(() => {
    const term = query.trim().toLowerCase();
    return (catalog.data ?? []).filter((row) =>
      !term
        ? true
        : row.agent.name.toLowerCase().includes(term) ||
          row.agent.id.toLowerCase().includes(term) ||
          (row.version?.spec.model?.provider ?? "").toLowerCase().includes(term) ||
          (row.version?.spec.model?.name ?? "").toLowerCase().includes(term),
    );
  }, [catalog.data, query]);

  return (
    <>
      <PageHeader
        title="Agents"
        description="Immutable agent versions pinned by workflow executions. Provider and model come from the latest version."
        actions={
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-2 size-3.5 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Filter agents"
              className="h-8 w-52 pl-8 text-xs"
              aria-label="Filter agents"
            />
          </div>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Agent catalog</CardTitle>
          <CardDescription>
            Execution counts and last run are derived from workflows that pin the agent.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {catalog.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading agents…</p>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={Bot}
              title="No agents"
              description="Create an agent through the SDK or CLI; it will appear here with its versions and execution history."
              className="m-4"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Agent</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Provider / model</TableHead>
                  <TableHead className="hidden lg:table-cell">Executions</TableHead>
                  <TableHead className="hidden xl:table-cell">Last execution</TableHead>
                  <TableHead className="w-20 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map(({ agent, version, executionCount, lastExecutionAt }) => (
                  <TableRow key={agent.id}>
                    <TableCell className="max-w-[18rem]">
                      <Link
                        href={`/agents/${agent.id}`}
                        className="block truncate text-sm font-medium hover:underline"
                      >
                        {agent.name}
                      </Link>
                      <p className="truncate text-xs text-muted-foreground">
                        {agent.description || agent.id}
                      </p>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className="font-mono text-[10px]">
                        v{agent.latest_version}
                      </Badge>
                      <span className="ml-2 text-xs">
                        <StatusIndicator status={version?.status ?? "draft"} />
                      </span>
                    </TableCell>
                    <TableCell className="text-xs">
                      <p className="font-mono">{version?.spec.model?.provider ?? "—"}</p>
                      <p className="text-muted-foreground">{version?.spec.model?.name ?? "—"}</p>
                    </TableCell>
                    <TableCell className="hidden text-sm tabular-nums text-muted-foreground lg:table-cell">
                      {executionCount}
                    </TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground xl:table-cell">
                      {formatRelative(lastExecutionAt)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Link
                        href={`/agents/${agent.id}`}
                        className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                      >
                        Inspect <ExternalLink className="size-3" aria-hidden="true" />
                      </Link>
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
