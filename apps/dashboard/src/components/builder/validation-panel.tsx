"use client";

import { AlertTriangle, CheckCircle2, RefreshCw } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { ValidationIssue } from "@/lib/builder/graph";
import { cn } from "@/lib/utils";

export function ValidationPanel({
  issues,
  valid,
  validating,
  onValidate,
  onFocus,
}: {
  issues: ValidationIssue[];
  valid: boolean | null;
  validating: boolean;
  onValidate: () => void;
  onFocus: (nodeId: string) => void;
}) {
  const errors = issues.filter((issue) => issue.source === "local" || issue.source === "server");
  return (
    <div className="space-y-3 p-4">
      <div className="flex items-center justify-between gap-2">
        <div>
          <p className="text-sm font-medium">Validation</p>
          <p className="text-[11px] text-muted-foreground">
            Local checks run on every edit; the server is authoritative on save and deploy.
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={onValidate} disabled={validating}>
          <RefreshCw className={cn("size-3.5", validating && "animate-spin")} />
          {validating ? "Checking" : "Validate"}
        </Button>
      </div>

      {valid === true ? (
        <div className="flex items-center gap-2 rounded-md border border-success/30 bg-success/5 p-2 text-xs text-success">
          <CheckCircle2 className="size-4" /> Graph is valid on the server.
        </div>
      ) : null}

      {errors.length === 0 ? (
        <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
          {valid === false
            ? "The server rejected the graph. Check the graph and validate again."
            : "No local problems found. Validate against the server before deploying."}
        </p>
      ) : (
        <ul className="space-y-1.5">
          {errors.map((issue) => (
            <li key={issue.id}>
              <button
                type="button"
                disabled={!issue.nodeId}
                onClick={() => {
                  if (issue.nodeId) onFocus(issue.nodeId);
                }}
                className={cn(
                  "flex w-full items-start gap-2 rounded-md border p-2 text-left text-[11px]",
                  issue.nodeId
                    ? "border-danger/30 bg-danger/5 hover:border-danger/60"
                    : "border-border bg-muted/30",
                )}
              >
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-danger" />
                <span className="min-w-0 flex-1">
                  <span className="block leading-relaxed">{issue.message}</span>
                  <span className="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
                    <Badge variant="neutral">{issue.source}</Badge>
                    {issue.nodeId ? <span className="font-mono">{issue.nodeId}</span> : null}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
