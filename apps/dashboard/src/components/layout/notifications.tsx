"use client";

import { Bell, CircleAlert, ClipboardCheck } from "lucide-react";
import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { useApprovals, useExecutions, useWorkspaceScope } from "@/lib/api/hooks";
import { formatRelative } from "@/lib/format";

export function Notifications() {
  const { orgId, projectId } = useWorkspaceScope();
  const approvals = useApprovals(orgId, projectId, "pending");
  const failures = useExecutions(orgId, projectId, { state: "failed", limit: 8 });
  const pending = approvals.data?.items ?? [];
  const failed = failures.data?.items ?? [];
  const count = pending.length + failed.length;

  return (
    <details className="group relative">
      <summary
        className="relative grid size-9 cursor-pointer list-none place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground [&::-webkit-details-marker]:hidden"
        aria-label={`Notifications${count ? `, ${count} unread` : ""}`}
      >
        <Bell className="size-4" aria-hidden="true" />
        {count > 0 ? (
          <span className="absolute right-1.5 top-1.5 grid min-w-4 place-items-center rounded-full bg-danger px-1 text-[9px] font-semibold text-white">
            {count > 9 ? "9+" : count}
          </span>
        ) : null}
      </summary>
      <div className="absolute right-0 z-50 mt-2 w-80 rounded-lg border border-border bg-card p-2 shadow-xl">
        <div className="flex items-center justify-between px-2 py-1.5">
          <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Notifications
          </p>
          <Badge variant="outline">{count}</Badge>
        </div>
        {count === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">All caught up.</p>
        ) : (
          <ul className="max-h-80 space-y-0.5 overflow-y-auto">
            {pending.slice(0, 5).map((approval) => (
              <li key={approval.id}>
                <Link
                  href={`/executions/${approval.execution_id ?? ""}`}
                  className="flex gap-2 rounded-md px-2 py-2 hover:bg-muted"
                >
                  <ClipboardCheck className="mt-0.5 size-3.5 shrink-0 text-warning" />
                  <span className="min-w-0">
                    <span className="block truncate text-xs">Approval required</span>
                    <span className="block truncate text-[11px] text-muted-foreground">
                      {approval.prompt || approval.task_id} ·{" "}
                      {formatRelative(approval.requested_at)}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
            {failed.slice(0, 5).map((execution) => (
              <li key={execution.id}>
                <Link
                  href={`/executions/${execution.id}`}
                  className="flex gap-2 rounded-md px-2 py-2 hover:bg-muted"
                >
                  <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-danger" />
                  <span className="min-w-0">
                    <span className="block truncate text-xs">Execution failed</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">
                      {execution.id} · {formatRelative(execution.created_at)}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </details>
  );
}
