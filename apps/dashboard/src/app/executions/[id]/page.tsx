"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Ban, CheckCircle2, Radio, RotateCcw, XCircle } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { use, useEffect, useMemo, useState } from "react";

import { EventTimeline } from "@/components/executions/event-timeline";
import { FailureInspector } from "@/components/executions/failure-inspector";
import { NodeInspector } from "@/components/executions/node-inspector";
import { WorkflowGraph } from "@/components/graph/workflow-graph";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { TabPanel, Tabs } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { api, apiPaths, newIdempotencyKey } from "@/lib/api";
import {
  useEnvironments,
  useExecution,
  useExecutionApprovals,
  useExecutionEvents,
  useExecutionInput,
  useExecutionOutputs,
  useExecutionTasks,
  useTaskDetail,
  useWorkflow,
  useWorkflowVersion,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import { useExecutionStream } from "@/lib/api/stream";
import type { Execution } from "@/lib/api/types";
import { formatDurationBetween } from "@/lib/format";
import { redactedJson } from "@/lib/redact";
import { formatDateTime, formatDuration } from "@/lib/utils";
import { useWorkspace } from "@/stores/workspace";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);
const TABS = [
  { value: "graph", label: "Graph" },
  { value: "timeline", label: "Timeline" },
  { value: "logs", label: "Logs" },
  { value: "failure", label: "Failure" },
];

export default function ExecutionDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const queryClient = useQueryClient();
  const { orgId, projectId } = useWorkspaceScope();
  const environmentId = useWorkspace((state) => state.environmentId);
  const execution = useExecution(orgId, projectId, id);
  const tasks = useExecutionTasks(orgId, projectId, id);
  const events = useExecutionEvents(orgId, projectId, id);
  const input = useExecutionInput(orgId, projectId, id);
  const outputs = useExecutionOutputs(orgId, projectId, id);
  const approvals = useExecutionApprovals(orgId, projectId, id);
  const workflow = useWorkflow(orgId, projectId, execution.data?.workflow_id ?? "");
  const workflowVersion = useWorkflowVersion(
    orgId,
    projectId,
    execution.data?.workflow_id ?? "",
    execution.data?.workflow_version ?? 0,
  );
  const environments = useEnvironments(orgId, projectId);
  const [tab, setTab] = useState("graph");
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  const [logQuery, setLogQuery] = useState("");
  const [logType, setLogType] = useState("");
  const [logNodeType, setLogNodeType] = useState("");
  const [busy, setBusy] = useState(false);
  const live = useExecutionStream(
    orgId,
    projectId,
    id,
    Boolean(execution.data && !TERMINAL.has(execution.data.state)),
  );

  const taskList = tasks.data?.items ?? [];
  const selectedTask = useTaskDetail(orgId, projectId, id, selectedTaskId ?? "");
  const selectedNode = workflowVersion.data?.graph.nodes.find((node) => node.id === selectedNodeId);
  const environment = environments.data?.items.find((item) => item.id === environmentId);

  useEffect(() => {
    if (taskList.length === 0) return;
    const failed = taskList.find((task) => task.state === "failed");
    const preferred =
      failed ??
      taskList.find((task) => task.state === "running") ??
      taskList.find((task) => task.node_type === "agent" || task.node_type === "llm") ??
      taskList[0];
    if (!selectedNodeId || !taskList.some((task) => task.node_id === selectedNodeId)) {
      setSelectedNodeId(preferred?.node_id ?? null);
      setSelectedTaskId(preferred?.id ?? null);
    }
  }, [selectedNodeId, taskList]);

  const value = execution.data;
  const duration = value ? formatDurationBetween(value.started_at, value.ended_at) : null;
  const pendingApprovals = (approvals.data?.items ?? []).filter(
    (item) => item.status === "pending",
  );

  const nodeTypeByNode = useMemo(
    () => new Map(taskList.map((task) => [task.node_id, task.node_type])),
    [taskList],
  );
  const nodeTypes = useMemo(
    () => [...new Set(taskList.map((task) => task.node_type))].sort(),
    [taskList],
  );
  const filteredEvents = useMemo(() => {
    const term = logQuery.trim().toLowerCase();
    return (events.data ?? []).filter((event) => {
      if (logType && event.event_type !== logType) return false;
      if (logNodeType) {
        const nodeId = typeof event.payload?.node_id === "string" ? event.payload.node_id : "";
        if (nodeTypeByNode.get(nodeId) !== logNodeType) return false;
      }
      if (!term) return true;
      return (
        event.event_type.toLowerCase().includes(term) ||
        event.entity_id.toLowerCase().includes(term) ||
        (event.reason ?? "").toLowerCase().includes(term)
      );
    });
  }, [events.data, logNodeType, logQuery, logType, nodeTypeByNode]);
  const eventTypes = useMemo(
    () => [...new Set((events.data ?? []).map((event) => event.event_type))].sort(),
    [events.data],
  );
  const failedTasks = taskList.filter((task) => task.state === "failed");
  const incomingIds = [
    ...new Set([
      ...(selectedNode?.input_from ?? []),
      ...(workflowVersion.data?.graph.edges
        .filter((edge) => edge.to === selectedNodeId)
        .map((edge) => edge.from) ?? []),
    ]),
  ];
  const nodeInputs = Object.fromEntries(
    incomingIds
      .map((nodeId) => [nodeId, outputs.data?.[nodeId]])
      .filter(([, value]) => value !== undefined),
  );

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["execution", orgId, projectId, id] });
    await queryClient.invalidateQueries({ queryKey: ["tasks", orgId, projectId, id] });
    await queryClient.invalidateQueries({ queryKey: ["events", orgId, projectId, id] });
    await queryClient.invalidateQueries({
      queryKey: ["execution-approvals", orgId, projectId, id],
    });
  }

  async function cancelExecution() {
    setBusy(true);
    try {
      await api(`${apiPaths.execution(orgId, projectId, id)}:cancel`, {
        method: "POST",
        json: { reason: "Cancelled from dashboard" },
      });
      toast.success("Cancellation requested");
      await refresh();
    } catch (caught) {
      toast.error(caught instanceof Error ? caught.message : "Could not cancel execution");
    } finally {
      setBusy(false);
    }
  }

  async function resumeExecution() {
    setBusy(true);
    try {
      const resumed = await api<Execution>(`${apiPaths.execution(orgId, projectId, id)}:resume`, {
        method: "POST",
        json: { priority: 0 },
        idempotencyKey: newIdempotencyKey("dashboard-resume"),
      });
      toast.success("Execution resumed as a new run");
      router.push(`/executions/${resumed.id}`);
    } catch (caught) {
      toast.error(caught instanceof Error ? caught.message : "Could not resume execution");
    } finally {
      setBusy(false);
    }
  }

  async function decide(taskId: string, action: "approve" | "reject") {
    setBusy(true);
    try {
      await api(`${apiPaths.task(orgId, projectId, id, taskId)}:${action}`, {
        method: "POST",
        json: {
          comment: action === "approve" ? "Approved from dashboard" : "Rejected from dashboard",
        },
      });
      toast.success(`Approval ${action === "approve" ? "recorded" : "rejected"}`);
      await refresh();
    } catch (caught) {
      toast.error(caught instanceof Error ? caught.message : "Could not record decision");
    } finally {
      setBusy(false);
    }
  }

  if (execution.isPending) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }
  if (execution.isError || !value) {
    return (
      <EmptyState
        icon={Ban}
        title="Execution not found"
        description="The execution may belong to another project or the API key may be scoped elsewhere."
        action={
          <Link href="/executions" className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ArrowLeft className="size-3.5" /> Executions
          </Link>
        }
      />
    );
  }

  return (
    <>
      <div className="mb-3">
        <Link
          href="/executions"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" /> Executions
        </Link>
      </div>
      <PageHeader
        title={workflow.data?.name ?? value.workflow_id}
        description={`${value.id} · workflow v${value.workflow_version} · ${value.created_by}`}
        actions={
          <>
            <StatusIndicator status={value.state} />
            {live.state === "live" ? (
              <Badge variant="success" className="gap-1">
                <Radio className="size-3" /> live
              </Badge>
            ) : live.state === "reconnecting" ? (
              <Badge variant="warning">reconnecting</Badge>
            ) : null}
            {["pending", "assigned", "running", "failing"].includes(value.state) ? (
              <Button variant="outline" size="sm" onClick={cancelExecution} disabled={busy}>
                <Ban className="size-3.5" /> Cancel
              </Button>
            ) : null}
            {["failed", "cancelled"].includes(value.state) ? (
              <Button size="sm" onClick={resumeExecution} disabled={busy}>
                <RotateCcw className="size-3.5" /> {busy ? "Working…" : "Resume"}
              </Button>
            ) : null}
          </>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <Info
          label="Started"
          value={value.started_at ? formatDateTime(value.started_at) : "not started"}
        />
        <Info label="Duration" value={duration === null ? "—" : formatDuration(duration)} />
        <Info label="Environment" value={environment?.name ?? "project default"} />
        <Info label="Trigger" value={value.created_by} />
        <Info label="Nodes" value={`${value.counts.succeeded}/${value.counts.total} succeeded`} />
      </div>

      {pendingApprovals.length > 0 ? (
        <Card className="mt-6 border-warning/40 bg-warning/5">
          <CardHeader>
            <CardTitle>Approval required</CardTitle>
            <CardDescription>
              This execution is waiting on a human decision. Approvals do not occupy a worker slot.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {pendingApprovals.map((approval) => (
              <div
                key={approval.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-card p-3"
              >
                <div>
                  <p className="text-sm">{approval.prompt || "Approval requested"}</p>
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    policy {approval.policy} ·{" "}
                    {approval.approvers.length ? approval.approvers.join(", ") : "any operator"} ·
                    expires {approval.expires_at ? formatDateTime(approval.expires_at) : "never"}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    onClick={() => decide(approval.task_id, "approve")}
                    disabled={busy}
                  >
                    <CheckCircle2 className="size-3.5" /> Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => decide(approval.task_id, "reject")}
                    disabled={busy}
                  >
                    <XCircle className="size-3.5" /> Reject
                  </Button>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      <Tabs
        value={tab}
        onValueChange={setTab}
        items={TABS.map((item) => ({
          ...item,
          count:
            item.value === "failure"
              ? failedTasks.length
              : item.value === "logs"
                ? events.data?.length
                : undefined,
        }))}
        className="mt-6"
      />

      <TabPanel>
        {tab === "graph" ? (
          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_24rem]">
            <Card>
              <CardHeader className="flex-row items-center justify-between">
                <div>
                  <CardTitle>Workflow graph</CardTitle>
                  <CardDescription>
                    Node state is live. Click a node to inspect its attempts and output.
                  </CardDescription>
                </div>
                <Badge variant="outline">{value.counts.running} running</Badge>
              </CardHeader>
              <CardContent>
                {workflowVersion.isPending ? (
                  <Skeleton className="h-[440px] w-full" />
                ) : (
                  <WorkflowGraph
                    graph={workflowVersion.data?.graph}
                    tasks={taskList}
                    selectedNodeId={selectedNodeId}
                    onSelectNode={(nodeId) => {
                      setSelectedNodeId(nodeId);
                      setSelectedTaskId(
                        taskList.find((task) => task.node_id === nodeId)?.id ?? null,
                      );
                    }}
                  />
                )}
              </CardContent>
            </Card>
            <NodeInspector
              orgId={orgId}
              projectId={projectId}
              execution={value}
              node={selectedNode}
              taskId={selectedTaskId}
              executionInput={input.data?.input}
              nodeInputs={nodeInputs}
            />
          </div>
        ) : null}

        {tab === "timeline" ? (
          <Card>
            <CardHeader>
              <CardTitle>Execution timeline</CardTitle>
              <CardDescription>
                Persisted events in chronological order. The stream reconnects from the last event
                id.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <EventTimeline
                events={events.data ?? []}
                onSelectEntity={(entityId) => {
                  const task = taskList.find((item) => item.id === entityId);
                  if (task) {
                    setSelectedTaskId(task.id);
                    setSelectedNodeId(task.node_id);
                    setTab("graph");
                  }
                }}
              />
            </CardContent>
          </Card>
        ) : null}

        {tab === "logs" ? (
          <Card>
            <CardHeader>
              <CardTitle>Logs</CardTitle>
              <CardDescription>
                Execution-scoped audit log. Worker process logs are not persisted; task attempts
                carry the durable diagnostics.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex flex-wrap gap-2">
                <Input
                  value={logQuery}
                  onChange={(event) => setLogQuery(event.target.value)}
                  placeholder="Search event, entity id or reason"
                  className="h-8 max-w-sm text-xs"
                  aria-label="Search logs"
                />
                <Select
                  value={logType}
                  onChange={(event) => setLogType(event.target.value)}
                  aria-label="Event type"
                >
                  <option value="">All events</option>
                  {eventTypes.map((type) => (
                    <option key={type} value={type}>
                      {type}
                    </option>
                  ))}
                </Select>
                <Select
                  value={logNodeType}
                  onChange={(event) => setLogNodeType(event.target.value)}
                  aria-label="Node type"
                >
                  <option value="">All node types</option>
                  {nodeTypes.map((type) => (
                    <option key={type} value={type}>
                      {type}
                    </option>
                  ))}
                </Select>
                <Badge variant="outline">{filteredEvents.length} lines</Badge>
              </div>
              <details className="rounded-md border border-border">
                <summary className="cursor-pointer px-3 py-2 text-xs text-muted-foreground">
                  Raw JSON
                </summary>
                <div className="border-t border-border p-2">
                  <CodeBlock code={redactedJson(filteredEvents)} maxHeight="24rem" />
                </div>
              </details>
              <EventTimeline events={filteredEvents} dense />
            </CardContent>
          </Card>
        ) : null}

        {tab === "failure" ? (
          <FailureInspector
            execution={value}
            tasks={taskList}
            events={events.data ?? []}
            selectedTask={selectedTask.data}
            selectedTaskId={selectedTaskId}
            onSelectTask={(taskId) => {
              setSelectedTaskId(taskId);
              const task = taskList.find((item) => item.id === taskId);
              if (task) setSelectedNodeId(task.node_id);
            }}
            onResume={resumeExecution}
            onCancel={cancelExecution}
            resuming={busy}
          />
        ) : null}
      </TabPanel>
    </>
  );
}

function Info({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <CardContent className="p-3">
        <p className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="mt-1 truncate text-xs">{value}</p>
      </CardContent>
    </Card>
  );
}
