"use client";

import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ClipboardCheck, XCircle } from "lucide-react";
import Link from "next/link";
import { useState } from "react";
import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Select } from "@/components/ui/select";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { api, apiPaths } from "@/lib/api";
import { useApprovals, useWorkspaceScope } from "@/lib/api/hooks";
import type { Approval } from "@/lib/api/types";
import { formatDateTime } from "@/lib/utils";

export default function ApprovalsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const [status, setStatus] = useState("pending");
  const approvals = useApprovals(orgId, projectId, status);
  const [busy, setBusy] = useState<string | null>(null);
  const queryClient = useQueryClient();

  async function decide(approval: Approval, action: "approve" | "reject") {
    if (!approval.execution_id) return;
    setBusy(approval.id);
    try {
      await api(
        `${apiPaths.task(orgId, projectId, approval.execution_id, approval.task_id)}:${action}`,
        {
          method: "POST",
          json: {
            comment: action === "approve" ? "Approved from dashboard" : "Rejected from dashboard",
          },
        },
      );
      toast.success(`Approval ${action === "approve" ? "recorded" : "rejected"}`);
      await queryClient.invalidateQueries({ queryKey: ["approvals"] });
      await queryClient.invalidateQueries({ queryKey: ["execution-approvals"] });
    } catch (caught) {
      toast.error(caught instanceof Error ? caught.message : "Could not record decision");
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <PageHeader
        title="Approvals"
        description="Human decisions requested by approval nodes. Pending approvals keep their execution open without occupying a worker slot."
      />

      <Card>
        <CardHeader className="gap-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle>Approval queue</CardTitle>
              <CardDescription>
                Decisions apply to the pinned workflow version and are recorded in the event
                history.
              </CardDescription>
            </div>
            <Select
              value={status}
              onChange={(event) => setStatus(event.target.value)}
              aria-label="Approval status"
            >
              <option value="pending">Pending</option>
              <option value="approved">Approved</option>
              <option value="rejected">Rejected</option>
              <option value="expired">Expired</option>
            </Select>
          </div>
          <Tabs
            value={status}
            onValueChange={setStatus}
            items={[
              { value: "pending", label: "Pending" },
              { value: "approved", label: "Approved" },
              { value: "rejected", label: "Rejected" },
              { value: "expired", label: "Expired" },
            ]}
          />
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {approvals.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading approvals…</p>
          ) : (approvals.data?.items ?? []).length === 0 ? (
            <EmptyState
              icon={ClipboardCheck}
              title="Nothing to review"
              description="When a workflow reaches an approval node, the request appears here."
              className="m-4"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Prompt</TableHead>
                  <TableHead>Execution</TableHead>
                  <TableHead className="hidden md:table-cell">Policy</TableHead>
                  <TableHead className="hidden lg:table-cell">Requested</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-40 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(approvals.data?.items ?? []).map((approval) => (
                  <TableRow key={approval.id}>
                    <TableCell className="max-w-[18rem]">
                      <p className="truncate text-sm">{approval.prompt || "Approval requested"}</p>
                      <p className="truncate font-mono text-[11px] text-muted-foreground">
                        {approval.approvers.length ? approval.approvers.join(", ") : "any operator"}
                        {approval.expires_at
                          ? ` · expires ${formatDateTime(approval.expires_at)}`
                          : ""}
                      </p>
                    </TableCell>
                    <TableCell className="max-w-[14rem]">
                      {approval.execution_id ? (
                        <Link
                          href={`/executions/${approval.execution_id}`}
                          className="block truncate font-mono text-xs text-primary hover:underline"
                        >
                          {approval.execution_id}
                        </Link>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                      <p className="truncate font-mono text-[10px] text-muted-foreground">
                        {approval.task_id}
                      </p>
                    </TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                      {approval.policy}
                    </TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">
                      {formatDateTime(approval.requested_at)}
                    </TableCell>
                    <TableCell>
                      <StatusIndicator status={approval.status} />
                      {approval.decided_by ? (
                        <p className="mt-0.5 truncate text-[10px] text-muted-foreground">
                          {approval.decided_by}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-right">
                      {approval.status === "pending" && approval.execution_id ? (
                        <div className="flex justify-end gap-1.5">
                          <Button
                            size="sm"
                            disabled={busy === approval.id}
                            onClick={() => decide(approval, "approve")}
                          >
                            <CheckCircle2 className="size-3.5" /> Approve
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busy === approval.id}
                            onClick={() => decide(approval, "reject")}
                          >
                            <XCircle className="size-3.5" /> Reject
                          </Button>
                        </div>
                      ) : (
                        <Badge variant="outline">
                          {approval.decided_at ? formatDateTime(approval.decided_at) : "—"}
                        </Badge>
                      )}
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
