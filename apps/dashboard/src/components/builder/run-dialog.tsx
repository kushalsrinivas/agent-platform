"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { api, apiPaths, newIdempotencyKey } from "@/lib/api";
import type { Execution, WorkflowGraph, WorkflowVersion } from "@/lib/api/types";

function skeleton(schema: unknown): Record<string, unknown> {
  if (!schema || typeof schema !== "object") return {};
  const record = schema as Record<string, unknown>;
  if (record.type && record.type !== "object") return {};
  const properties =
    (record.properties as Record<string, Record<string, unknown>> | undefined) ?? {};
  const result: Record<string, unknown> = {};
  for (const [key, property] of Object.entries(properties)) {
    if (property.type === "array") result[key] = [];
    else if (property.type === "number" || property.type === "integer") result[key] = 0;
    else if (property.type === "boolean") result[key] = false;
    else if (property.type === "object") result[key] = {};
    else result[key] = "";
  }
  return result;
}

export function RunDialog({
  open,
  onOpenChange,
  orgId,
  projectId,
  workflowId,
  graph,
  versions,
  defaultVersion,
  onStarted,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  orgId: string;
  projectId: string;
  workflowId: string;
  graph: WorkflowGraph;
  versions: WorkflowVersion[];
  defaultVersion: number | null;
  onStarted: (executionId: string) => void;
}) {
  const queryClient = useQueryClient();
  const published = useMemo(
    () => versions.filter((version) => version.status === "published"),
    [versions],
  );
  const [version, setVersion] = useState("");
  const [input, setInput] = useState("{}");
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);

  useEffect(() => {
    if (!open) return;
    const preferred =
      published.find((item) => item.version === defaultVersion) ??
      published.at(-1) ??
      published.at(0);
    setVersion(String(preferred?.version ?? ""));
    setInput(JSON.stringify(skeleton(graph.input_schema), null, 2));
    setError(null);
  }, [defaultVersion, graph.input_schema, open, published]);

  async function start(event: React.FormEvent) {
    event.preventDefault();
    const selected = Number(version);
    if (!selected) {
      setError("Deploy a published version first.");
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
    setStarting(true);
    setError(null);
    try {
      const execution = await api<Execution>(apiPaths.executions(orgId, projectId), {
        method: "POST",
        json: {
          workflow_id: workflowId,
          workflow_version: selected,
          input: parsed,
        },
        idempotencyKey: newIdempotencyKey("builder-run"),
      });
      await queryClient.invalidateQueries({ queryKey: ["executions"] });
      onStarted(execution.id);
      onOpenChange(false);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not start execution.");
    } finally {
      setStarting(false);
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Run workflow"
      description="Starts a published version from the builder. The execution pins this exact version."
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="builder-run-form"
            disabled={starting || published.length === 0}
          >
            <Play className="size-3.5" /> {starting ? "Starting…" : "Run"}
          </Button>
        </>
      }
    >
      <form id="builder-run-form" onSubmit={start} className="space-y-3">
        {published.length === 0 ? (
          <p className="rounded-md border border-warning/30 bg-warning/5 p-2 text-xs text-warning">
            No published version exists yet. Save and deploy the workflow first.
          </p>
        ) : null}
        <label className="block space-y-1">
          <span className="text-xs font-medium">Version</span>
          <select
            value={version}
            onChange={(event) => setVersion(event.target.value)}
            className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm"
          >
            {published.map((item) => (
              <option key={item.id} value={item.version}>
                v{item.version}
                {item.version === defaultVersion ? " · deployed" : ""}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1">
          <span className="text-xs font-medium">Input (JSON object)</span>
          <textarea
            value={input}
            onChange={(event) => setInput(event.target.value)}
            rows={9}
            spellCheck={false}
            className="w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
          />
        </label>
        {error ? <p className="text-xs text-danger">{error}</p> : null}
        <p className="text-[11px] text-muted-foreground">
          Inputs are immutable once accepted. The run appears in the execution explorer and the live
          panel.
        </p>
      </form>
    </Modal>
  );
}
