"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { api, apiPaths, newIdempotencyKey } from "@/lib/api";
import { useWorkflowCatalog } from "@/lib/api/catalog";
import { useWorkspaceScope } from "@/lib/api/hooks";
import type { Execution } from "@/lib/api/types";

export function NewExecutionDialog() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const { orgId, projectId } = useWorkspaceScope();
  const [open, setOpen] = useState(false);
  const catalog = useWorkflowCatalog(orgId, projectId, open);
  const [workflowId, setWorkflowId] = useState("");
  const [input, setInput] = useState('{\n  "topic": "Durable execution patterns"\n}');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const published = useMemo(
    () => (catalog.data ?? []).filter((row) => row.version?.status === "published"),
    [catalog.data],
  );
  const selected = published.find((row) => row.workflow.id === workflowId) ?? published[0];

  useEffect(() => {
    if (!workflowId && published[0]) setWorkflowId(published[0].workflow.id);
  }, [published, workflowId]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!selected?.version) {
      setError("Choose a published workflow.");
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(input);
    } catch {
      setError("Input must be valid JSON.");
      return;
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      setError("Input must be a JSON object.");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const execution = await api<Execution>(apiPaths.executions(orgId, projectId), {
        method: "POST",
        json: {
          workflow_id: selected.workflow.id,
          workflow_version: selected.version.version,
          input: parsed,
        },
        idempotencyKey: newIdempotencyKey("dashboard-new"),
      });
      await queryClient.invalidateQueries({ queryKey: ["executions"] });
      setOpen(false);
      router.push(`/executions/${execution.id}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not submit execution.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>New execution</Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="New execution"
        description="Start a published workflow version with immutable JSON input."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" form="new-execution-form" disabled={submitting}>
              {submitting ? "Submitting…" : "Run workflow"}
            </Button>
          </>
        }
      >
        <form id="new-execution-form" onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <label htmlFor="workflow" className="text-sm font-medium">
              Workflow
            </label>
            <select
              id="workflow"
              value={selected?.workflow.id ?? ""}
              onChange={(event) => setWorkflowId(event.target.value)}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              {published.map((row) => (
                <option key={row.workflow.id} value={row.workflow.id}>
                  {row.workflow.name} · v{row.version?.version}
                </option>
              ))}
            </select>
            {published.length === 0 ? (
              <p className="text-xs text-danger">No published workflow is available.</p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <label htmlFor="execution-input" className="text-sm font-medium">
              Input (JSON object)
            </label>
            <textarea
              id="execution-input"
              value={input}
              onChange={(event) => setInput(event.target.value)}
              rows={8}
              spellCheck={false}
              className="scrollbar-thin w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
            />
            {error ? <p className="text-xs text-danger">{error}</p> : null}
          </div>

          <p className="text-xs text-muted-foreground">
            Inputs are immutable once accepted. Maximum size is 256 KiB. The SDK and CLI generate
            the same idempotency key pattern.
          </p>
        </form>
      </Modal>
    </>
  );
}
