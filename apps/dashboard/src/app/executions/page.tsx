"use client";

import { Search } from "lucide-react";
import { useMemo, useState } from "react";

import { defaultRange, type TimeRange, TimeRangePicker } from "@/components/dashboard/time-range";
import { ExecutionsTable } from "@/components/executions/executions-table";
import { NewExecutionDialog } from "@/components/executions/new-execution-dialog";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { useExecutions, useWorkflows, useWorkspaceScope } from "@/lib/api/hooks";

const STATES = [
  { value: "", label: "All states" },
  { value: "running", label: "Running" },
  { value: "pending", label: "Queued" },
  { value: "succeeded", label: "Succeeded" },
  { value: "failed", label: "Failed" },
  { value: "cancelled", label: "Cancelled" },
];

export default function ExecutionsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const [state, setState] = useState("");
  const [workflowId, setWorkflowId] = useState("");
  const [query, setQuery] = useState("");
  const [range, setRange] = useState<TimeRange>(() => defaultRange("7d"));
  const executions = useExecutions(orgId, projectId, {
    state: state || undefined,
    workflowId: workflowId || undefined,
    createdAfter: range.from,
    createdBefore: range.to,
    limit: 100,
  });
  const workflows = useWorkflows(orgId, projectId);
  const workflowNames = useMemo(
    () =>
      Object.fromEntries(
        (workflows.data?.items ?? []).map((workflow) => [workflow.id, workflow.name]),
      ),
    [workflows.data],
  );
  const rows = useMemo(() => {
    const term = query.trim().toLowerCase();
    return (executions.data?.items ?? []).filter((execution) =>
      !term
        ? true
        : execution.id.toLowerCase().includes(term) ||
          execution.workflow_id.toLowerCase().includes(term) ||
          (workflowNames[execution.workflow_id] ?? "").toLowerCase().includes(term),
    );
  }, [executions.data, query, workflowNames]);
  const counts = useMemo(() => {
    const items = executions.data?.items ?? [];
    return {
      active: items.filter((item) => ["running", "pending", "assigned"].includes(item.state))
        .length,
      failed: items.filter((item) => item.state === "failed").length,
      succeeded: items.filter((item) => item.state === "succeeded").length,
    };
  }, [executions.data]);

  return (
    <>
      <PageHeader
        title="Executions"
        description="Every run of a published workflow version. Filter by status, workflow and time; open a run to inspect the graph and failure."
        actions={<NewExecutionDialog />}
      />

      <Card>
        <CardHeader className="gap-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle>Execution history</CardTitle>
              <CardDescription>
                {counts.active} active · {counts.succeeded} succeeded · {counts.failed} failed in
                this view
              </CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant="outline">{rows.length} rows</Badge>
              <Badge variant={counts.failed ? "danger" : "neutral"}>{counts.failed} failed</Badge>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative min-w-[13rem] flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-2 size-3.5 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search execution, workflow name or id"
                className="h-8 pl-8 text-xs"
                aria-label="Search executions"
              />
            </div>
            <Select
              value={state}
              onChange={(event) => setState(event.target.value)}
              aria-label="Status filter"
            >
              {STATES.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </Select>
            <Select
              value={workflowId}
              onChange={(event) => setWorkflowId(event.target.value)}
              aria-label="Workflow filter"
            >
              <option value="">All workflows</option>
              {(workflows.data?.items ?? []).map((workflow) => (
                <option key={workflow.id} value={workflow.id}>
                  {workflow.name}
                </option>
              ))}
            </Select>
            <TimeRangePicker value={range} onChange={setRange} />
          </div>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          <ExecutionsTable
            executions={rows}
            workflowNames={workflowNames}
            isLoading={executions.isPending}
          />
        </CardContent>
      </Card>
    </>
  );
}
