"use client";

import { ArrowLeft, Bot, KeyRound, Settings2, Wrench } from "lucide-react";
import Link from "next/link";
import { use, useMemo } from "react";

import { AgentRunPanel } from "@/components/agents/agent-run-panel";
import { AgentTestDialog } from "@/components/agents/agent-test-dialog";
import { ExecutionsTable } from "@/components/executions/executions-table";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
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
import { useAgentCatalog, useWorkflowCatalog } from "@/lib/api/catalog";
import { useAgent, useAgentVersions, useExecutions, useWorkspaceScope } from "@/lib/api/hooks";
import { formatDate, formatPercent, formatRelative } from "@/lib/format";
import { redactedJson } from "@/lib/redact";
import { formatDuration } from "@/lib/utils";

export default function AgentDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { orgId, projectId } = useWorkspaceScope();
  const agent = useAgent(orgId, projectId, id);
  const versions = useAgentVersions(orgId, projectId, id);
  const catalog = useAgentCatalog(orgId, projectId);
  const workflows = useWorkflowCatalog(orgId, projectId);
  const executions = useExecutions(orgId, projectId, { limit: 200 });
  const row = catalog.data?.find((item) => item.agent.id === id);
  const latest = useMemo(() => {
    const items = versions.data?.items ?? [];
    return items.find((item) => item.version === agent.data?.latest_version) ?? items.at(0);
  }, [agent.data?.latest_version, versions.data]);
  const workflowNames = useMemo(
    () =>
      Object.fromEntries(
        (workflows.data ?? []).map((item) => [item.workflow.id, item.workflow.name]),
      ),
    [workflows.data],
  );
  const history = (executions.data?.items ?? []).filter((execution) =>
    (row?.workflowIds ?? []).includes(execution.workflow_id),
  );
  const terminal = history.filter((execution) =>
    ["succeeded", "failed", "cancelled"].includes(execution.state),
  );
  const succeeded = terminal.filter((execution) => execution.state === "succeeded").length;
  const averageDuration =
    terminal.length === 0
      ? null
      : terminal.reduce((sum, execution) => {
          if (!execution.started_at || !execution.ended_at) return sum;
          return (
            sum +
            (new Date(execution.ended_at).getTime() - new Date(execution.started_at).getTime())
          );
        }, 0) / terminal.length;

  if (!orgId || !projectId || agent.isPending) {
    return <p className="text-sm text-muted-foreground">Loading agent…</p>;
  }
  if (versions.isPending) {
    return <p className="text-sm text-muted-foreground">Loading versions…</p>;
  }
  if (versions.isError) {
    return (
      <EmptyState
        icon={Bot}
        title="Could not load agent versions"
        description={
          versions.error instanceof Error
            ? versions.error.message
            : "The API did not return the version list."
        }
        action={
          <Button variant="outline" size="sm" onClick={() => versions.refetch()}>
            Retry
          </Button>
        }
      />
    );
  }
  if (agent.isError || !agent.data) {
    return (
      <EmptyState
        icon={Bot}
        title="Agent not found"
        description="The agent may have been deleted or belongs to another project."
        action={
          <Link href="/agents" className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ArrowLeft className="size-3.5" /> Agents
          </Link>
        }
      />
    );
  }

  const value = agent.data;
  const spec = latest?.spec;

  return (
    <>
      <div className="mb-3">
        <Link
          href="/agents"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" /> Agents
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
            {latest && <AgentTestDialog agent={value} version={latest} />}
          </>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="Executions" value={String(row?.executionCount ?? history.length)} />
        <Stat
          label="Success rate"
          value={formatPercent(terminal.length ? (succeeded / terminal.length) * 100 : null)}
        />
        <Stat
          label="Average duration"
          value={averageDuration === null ? "—" : formatDuration(Math.round(averageDuration))}
        />
        <Stat label="Last execution" value={formatRelative(row?.lastExecutionAt)} />
      </div>

      {latest ? (
        <div className="mt-6">
          <AgentRunPanel agent={value} version={latest} />
        </div>
      ) : null}

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle>Configuration</CardTitle>
            <CardDescription>
              Latest immutable version, provider settings and runtime.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <dl className="grid gap-3 text-sm sm:grid-cols-2">
              <Field label="Provider" value={spec?.model?.provider ?? "—"} />
              <Field label="Model" value={spec?.model?.name ?? "—"} />
              <Field label="Credential ref" value={spec?.model?.credential_ref ?? "—"} />
              <Field label="Runtime ABI" value={String(latest?.runtime_abi ?? "—")} />
              <Field label="Max turns" value={String(spec?.limits?.max_turns ?? "—")} />
              <Field
                label="Timeout"
                value={spec?.limits?.timeout_seconds ? `${spec.limits.timeout_seconds}s` : "—"}
              />
            </dl>
            <div>
              <p className="mb-1.5 text-xs font-medium text-muted-foreground">Instructions</p>
              <p className="rounded-md border border-border bg-muted/30 p-3 text-sm leading-relaxed">
                {spec?.instructions || "No instructions."}
              </p>
            </div>
            {spec?.output_schema ? (
              <div>
                <p className="mb-1.5 text-xs font-medium text-muted-foreground">Output schema</p>
                <CodeBlock code={redactedJson(spec.output_schema)} maxHeight="12rem" />
              </div>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Version history</CardTitle>
            <CardDescription>Published versions are immutable.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {(versions.data?.items ?? []).map((version) => (
              <div
                key={version.id}
                className="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2"
              >
                <div>
                  <p className="font-mono text-xs">v{version.version}</p>
                  <p className="text-[11px] text-muted-foreground">
                    {formatDate(version.created_at)}
                  </p>
                </div>
                <StatusIndicator status={version.status} />
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Wrench className="size-3.5" /> Tools
            </CardTitle>
            <CardDescription>Bound tools and runtime-enforced policies.</CardDescription>
          </CardHeader>
          <CardContent>
            {(spec?.tools ?? []).length === 0 ? (
              <p className="text-xs text-muted-foreground">No tools bound.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Tool</TableHead>
                    <TableHead>Policy</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(spec?.tools ?? []).map((tool) => (
                    <TableRow key={tool.tool_id}>
                      <TableCell className="font-mono text-xs">
                        {tool.tool_id} v{tool.version}
                      </TableCell>
                      <TableCell>
                        <CodeBlock code={redactedJson(tool.policy ?? {})} maxHeight="8rem" />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <KeyRound className="size-3.5" /> Permissions
            </CardTitle>
            <CardDescription>Tool and MCP policies are enforced by the runtime.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {(spec?.tools ?? []).map((tool) => (
              <div key={tool.tool_id} className="rounded-md border border-border p-3 text-xs">
                <p className="font-mono">{tool.tool_id}</p>
                <p className="mt-1 text-muted-foreground">
                  {Object.entries(
                    (tool.policy?.permissions as Record<string, boolean> | undefined) ?? {},
                  )
                    .map(([key, allowed]) => `${key}:${allowed ? "allow" : "deny"}`)
                    .join(" · ") || "default read-only policy"}
                </p>
              </div>
            ))}
            {(spec?.mcp_servers ?? []).map((server) => (
              <div key={server.name} className="rounded-md border border-border p-3 text-xs">
                <p className="font-mono">MCP {server.name}</p>
                <p className="mt-1 text-muted-foreground">
                  {server.transport} · {server.url ?? server.command}
                </p>
              </div>
            ))}
            {!spec?.tools?.length && !spec?.mcp_servers?.length ? (
              <p className="text-xs text-muted-foreground">No tool permissions declared.</p>
            ) : null}
          </CardContent>
        </Card>
      </div>

      {spec?.orchestration ? (
        <Card className="mt-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Settings2 className="size-3.5" /> Supervisor policy
            </CardTitle>
            <CardDescription>Allowed agents and delegation budgets.</CardDescription>
          </CardHeader>
          <CardContent>
            <CodeBlock code={redactedJson(spec.orchestration)} maxHeight="16rem" />
          </CardContent>
        </Card>
      ) : null}

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Execution history</CardTitle>
          <CardDescription>Runs of workflows that pin this agent.</CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          <ExecutionsTable executions={history} workflowNames={workflowNames} limit={20} />
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

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 font-mono text-xs">{value}</dd>
    </div>
  );
}
