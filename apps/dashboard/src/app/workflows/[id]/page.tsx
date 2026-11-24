"use client";

import { ArrowLeft, GitBranch } from "lucide-react";
import Link from "next/link";
import { use, useEffect, useMemo, useState } from "react";

import { ExecutionsTable } from "@/components/executions/executions-table";
import { WorkflowGraph } from "@/components/graph/workflow-graph";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
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
import {
  useExecutions,
  useWorkflow,
  useWorkflowVersion,
  useWorkflowVersions,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import { formatDate } from "@/lib/format";
import { redactedJson } from "@/lib/redact";

export default function WorkflowDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { orgId, projectId } = useWorkspaceScope();
  const workflow = useWorkflow(orgId, projectId, id);
  const versions = useWorkflowVersions(orgId, projectId, id);
  const [selectedVersion, setSelectedVersion] = useState(0);
  const version = useWorkflowVersion(orgId, projectId, id, selectedVersion);
  const executions = useExecutions(orgId, projectId, { workflowId: id, limit: 100 });

  useEffect(() => {
    if (selectedVersion === 0 && workflow.data?.latest_version)
      setSelectedVersion(workflow.data.latest_version);
  }, [selectedVersion, workflow.data?.latest_version]);

  const workflowNames = useMemo(
    () => (workflow.data ? { [workflow.data.id]: workflow.data.name } : {}),
    [workflow.data],
  );

  if (workflow.isPending) return <p className="text-sm text-muted-foreground">Loading workflow…</p>;
  if (workflow.isError || !workflow.data) {
    return (
      <EmptyState
        icon={GitBranch}
        title="Workflow not found"
        description="The workflow may belong to another project or have been removed."
        action={
          <Link href="/workflows" className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ArrowLeft className="size-3.5" /> Workflows
          </Link>
        }
      />
    );
  }

  const value = workflow.data;
  const graph = version.data?.graph;
  const nodeRows = graph?.nodes ?? [];

  return (
    <>
      <div className="mb-3">
        <Link
          href="/workflows"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" /> Workflows
        </Link>
      </div>
      <PageHeader
        title={value.name}
        description={value.description || value.id}
        actions={
          <>
            <Badge variant="outline" className="font-mono">
              v{value.latest_version}
            </Badge>
            {value.deployed_version ? (
              <Badge variant="success">deployed v{value.deployed_version}</Badge>
            ) : (
              <Badge variant="neutral">not deployed</Badge>
            )}
            <StatusIndicator status={version.data?.status ?? "draft"} />
            <Link href={`/workflows/${value.id}/edit`} className={buttonVariants({ size: "sm" })}>
              Open builder
            </Link>
          </>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="Nodes" value={String(graph?.nodes.length ?? "—")} />
        <Stat label="Edges" value={String(graph?.edges.length ?? "—")} />
        <Stat label="Executions" value={String(executions.data?.items.length ?? 0)} />
        <Stat label="Created" value={formatDate(value.created_at)} />
      </div>

      <Card className="mt-6">
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>Graph</CardTitle>
            <CardDescription>
              Published definition for v{selectedVersion || value.latest_version}. Click a node to
              inspect its configuration below.
            </CardDescription>
          </div>
          <select
            aria-label="Workflow version"
            value={selectedVersion || value.latest_version}
            onChange={(event) => setSelectedVersion(Number(event.target.value))}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
          >
            {(versions.data?.items ?? []).map((item) => (
              <option key={item.id} value={item.version}>
                v{item.version} · {item.status}
              </option>
            ))}
          </select>
        </CardHeader>
        <CardContent>
          <WorkflowGraph graph={graph} showStatus={false} className="h-[440px] w-full" />
        </CardContent>
      </Card>

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle>Node configuration</CardTitle>
            <CardDescription>Immutable graph node fields for the selected version.</CardDescription>
          </CardHeader>
          <CardContent className="px-2">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Node</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Reference</TableHead>
                  <TableHead className="hidden md:table-cell">Pool / timeout</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {nodeRows.map((node) => (
                  <TableRow key={node.id}>
                    <TableCell className="font-mono text-xs">{node.id}</TableCell>
                    <TableCell className="text-xs">{node.type}</TableCell>
                    <TableCell className="font-mono text-[11px] text-muted-foreground">
                      {node.agent
                        ? `${node.agent.agent_id} v${node.agent.version}`
                        : node.tool
                          ? `${node.tool.tool_id} v${node.tool.version}`
                          : "—"}
                    </TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                      {node.worker_pool ?? "—"}
                      {node.timeout_seconds ? ` · ${node.timeout_seconds}s` : ""}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle>Deployment</CardTitle>
              <CardDescription>Pinned version and publication time.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Latest</span>
                <span className="font-mono">v{value.latest_version}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Deployed</span>
                <span className="font-mono">
                  {value.deployed_version ? `v${value.deployed_version}` : "not deployed"}
                </span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Status</span>
                <StatusIndicator status={version.data?.status ?? "draft"} />
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Published</span>
                <span className="text-xs">{formatDate(version.data?.published_at)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Digest</span>
                <span className="max-w-[10rem] truncate font-mono text-[10px]">
                  {version.data?.digest ?? "—"}
                </span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Trigger settings</CardTitle>
              <CardDescription>How this workflow can start today.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2 text-xs">
              <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                <span>API / SDK / CLI</span>
                <Badge variant="success">enabled</Badge>
              </div>
              <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                <span>Scheduled trigger</span>
                <Badge variant="neutral">not configured</Badge>
              </div>
              <p className="text-muted-foreground">
                Schedules are driven by your external cron or scheduler using the idempotent execute
                API.
              </p>
            </CardContent>
          </Card>
        </div>
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle>Version history</CardTitle>
            <CardDescription>Draft, published and deprecated definitions.</CardDescription>
          </CardHeader>
          <CardContent className="px-2">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Version</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Published</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(versions.data?.items ?? []).map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="font-mono text-xs">
                      <button
                        type="button"
                        className="hover:underline"
                        onClick={() => setSelectedVersion(item.version)}
                      >
                        v{item.version}
                      </button>
                    </TableCell>
                    <TableCell>
                      <StatusIndicator status={item.status} />
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDate(item.created_at)}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDate(item.published_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Workflow policy</CardTitle>
            <CardDescription>Failure, retry and input contract.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <CodeBlock code={redactedJson(graph?.policy ?? {})} maxHeight="10rem" />
            <CodeBlock
              code={redactedJson(graph?.input_schema ?? {})}
              label="input_schema.json"
              maxHeight="10rem"
            />
          </CardContent>
        </Card>
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Execution history</CardTitle>
          <CardDescription>Every run pinned to this workflow.</CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          <ExecutionsTable
            executions={executions.data?.items ?? []}
            workflowNames={workflowNames}
            isLoading={executions.isPending}
          />
        </CardContent>
      </Card>
    </>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="mt-1.5 text-xl font-semibold tabular-nums">{value}</p>
      </CardContent>
    </Card>
  );
}
