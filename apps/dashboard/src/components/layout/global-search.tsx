"use client";

import { Search } from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";

import { Modal } from "@/components/ui/modal";
import { useAgents, useExecutions, useWorkflows, useWorkspaceScope } from "@/lib/api/hooks";

export function GlobalSearch({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { orgId, projectId } = useWorkspaceScope();
  const [query, setQuery] = useState("");
  const agents = useAgents(orgId, projectId);
  const workflows = useWorkflows(orgId, projectId);
  const executions = useExecutions(orgId, projectId, { limit: 100 });
  const term = query.trim().toLowerCase();

  const results = useMemo(() => {
    const rows: { href: string; title: string; detail: string; kind: string }[] = [];
    for (const execution of executions.data?.items ?? []) {
      if (
        !term ||
        execution.id.toLowerCase().includes(term) ||
        execution.workflow_id.toLowerCase().includes(term)
      ) {
        rows.push({
          href: `/executions/${execution.id}`,
          title: execution.id,
          detail: `${execution.workflow_id} · ${execution.state}`,
          kind: "execution",
        });
      }
    }
    for (const workflow of workflows.data?.items ?? []) {
      if (
        !term ||
        workflow.name.toLowerCase().includes(term) ||
        workflow.id.toLowerCase().includes(term)
      ) {
        rows.push({
          href: `/workflows/${workflow.id}`,
          title: workflow.name,
          detail: workflow.id,
          kind: "workflow",
        });
      }
    }
    for (const agent of agents.data?.items ?? []) {
      if (
        !term ||
        agent.name.toLowerCase().includes(term) ||
        agent.id.toLowerCase().includes(term)
      ) {
        rows.push({
          href: `/agents/${agent.id}`,
          title: agent.name,
          detail: agent.id,
          kind: "agent",
        });
      }
    }
    return rows.slice(0, 20);
  }, [agents.data, executions.data, term, workflows.data]);

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Search"
      description="Find an execution, workflow or agent by id or name."
    >
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground" />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="exec_…, workflow name, agent name"
          className="h-9 w-full rounded-md border border-input bg-background pl-9 pr-3 text-sm"
        />
      </div>
      <div className="mt-3 max-h-80 overflow-y-auto">
        {results.length === 0 ? (
          <p className="px-2 py-8 text-center text-xs text-muted-foreground">
            {term ? "No matching resources." : "Type to search, or pick a recent execution."}
          </p>
        ) : (
          <ul className="space-y-1">
            {results.map((result) => (
              <li key={`${result.kind}-${result.href}`}>
                <Link
                  href={result.href}
                  onClick={() => onOpenChange(false)}
                  className="flex items-center justify-between gap-4 rounded-md px-2 py-2 hover:bg-muted"
                >
                  <span className="min-w-0">
                    <span className="block truncate text-sm">{result.title}</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">
                      {result.detail}
                    </span>
                  </span>
                  <span className="shrink-0 text-[10px] uppercase tracking-wide text-muted-foreground">
                    {result.kind}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
      <p className="mt-3 text-[11px] text-muted-foreground">
        Results are filtered from the latest 100 executions and project resources.
      </p>
    </Modal>
  );
}
