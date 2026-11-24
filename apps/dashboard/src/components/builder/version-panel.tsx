"use client";

import { GitCompareArrows, History, Rocket } from "lucide-react";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { StatusIndicator } from "@/components/ui/status-indicator";
import { useWorkflowVersion } from "@/lib/api/hooks";
import type { WorkflowVersion } from "@/lib/api/types";
import { compareGraphs } from "@/lib/builder/graph";
import { formatDateTime } from "@/lib/utils";

export function VersionPanel({
  orgId,
  projectId,
  workflowId,
  versions,
  currentVersion,
  deployedVersion,
  onActivate,
  activating,
}: {
  orgId: string;
  projectId: string;
  workflowId: string;
  versions: WorkflowVersion[];
  currentVersion: number;
  deployedVersion: number | null;
  onActivate: (version: number) => void;
  activating: number | null;
}) {
  const [compareOpen, setCompareOpen] = useState(false);
  const [base, setBase] = useState<number | "">("");
  const [target, setTarget] = useState<number | "">("");

  return (
    <div className="space-y-3 p-4">
      <div>
        <p className="text-sm font-medium">Versions</p>
        <p className="text-[11px] text-muted-foreground">
          Drafts are immutable snapshots. Deploying marks the active version; older published
          versions can be re-activated as a rollback.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <select
          value={base}
          onChange={(event) => setBase(event.target.value ? Number(event.target.value) : "")}
          className="h-8 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-xs"
          aria-label="Compare base version"
        >
          <option value="">Base version</option>
          {versions.map((version) => (
            <option key={version.id} value={version.version}>
              v{version.version}
            </option>
          ))}
        </select>
        <span className="text-xs text-muted-foreground">→</span>
        <select
          value={target}
          onChange={(event) => setTarget(event.target.value ? Number(event.target.value) : "")}
          className="h-8 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-xs"
          aria-label="Compare target version"
        >
          <option value="">Target version</option>
          {versions.map((version) => (
            <option key={version.id} value={version.version}>
              v{version.version}
            </option>
          ))}
        </select>
        <Button
          size="sm"
          variant="outline"
          disabled={!base || !target || base === target}
          onClick={() => setCompareOpen(true)}
        >
          <GitCompareArrows className="size-3.5" /> Compare
        </Button>
      </div>

      <ul className="space-y-1.5">
        {versions.map((version) => {
          const active = deployedVersion === version.version;
          const current = currentVersion === version.version;
          return (
            <li key={version.id} className="rounded-md border border-border p-2.5 text-xs">
              <div className="flex items-center justify-between gap-2">
                <span className="flex items-center gap-2">
                  <span className="font-mono font-medium">v{version.version}</span>
                  <StatusIndicator status={version.status} />
                  {active ? <Badge variant="success">deployed</Badge> : null}
                  {current ? <Badge variant="outline">editing</Badge> : null}
                </span>
                {version.status === "published" && !active ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={activating === version.version}
                    onClick={() => onActivate(version.version)}
                  >
                    <Rocket className="size-3.5" />
                    {activating === version.version ? "Activating…" : "Activate"}
                  </Button>
                ) : null}
              </div>
              <p className="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground">
                <History className="size-3" />
                {formatDateTime(version.created_at)} · {version.digest.slice(0, 18)}…
              </p>
            </li>
          );
        })}
      </ul>

      <VersionCompareDialog
        open={compareOpen}
        onOpenChange={setCompareOpen}
        orgId={orgId}
        projectId={projectId}
        workflowId={workflowId}
        baseVersion={base}
        targetVersion={target}
      />
    </div>
  );
}

function VersionCompareDialog({
  open,
  onOpenChange,
  orgId,
  projectId,
  workflowId,
  baseVersion,
  targetVersion,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  orgId: string;
  projectId: string;
  workflowId: string;
  baseVersion: number | "";
  targetVersion: number | "";
}) {
  const base = useWorkflowVersion(orgId, projectId, workflowId, Number(baseVersion) || 0);
  const target = useWorkflowVersion(orgId, projectId, workflowId, Number(targetVersion) || 0);
  const diff = base.data && target.data ? compareGraphs(base.data.graph, target.data.graph) : null;

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={`Compare v${baseVersion || "?"} → v${targetVersion || "?"}`}
      description="Structural differences between two immutable workflow versions."
      footer={
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      {!diff ? (
        <p className="text-sm text-muted-foreground">Loading versions…</p>
      ) : (
        <div className="space-y-4 text-sm">
          <div className="flex flex-wrap gap-1.5">
            <Badge variant="success">+{diff.addedNodes.length} nodes</Badge>
            <Badge variant="danger">−{diff.removedNodes.length} nodes</Badge>
            <Badge variant="warning">{diff.changedNodes.length} changed</Badge>
            <Badge variant="success">+{diff.addedEdges.length} edges</Badge>
            <Badge variant="danger">−{diff.removedEdges.length} edges</Badge>
            <Badge variant="warning">{diff.changedEdges.length} edge changes</Badge>
          </div>
          <DiffSection title="Added nodes" values={diff.addedNodes} />
          <DiffSection title="Removed nodes" values={diff.removedNodes} />
          <DiffSection title="Changed nodes" values={diff.changedNodes} />
          <DiffSection title="Added edges" values={diff.addedEdges} />
          <DiffSection title="Removed edges" values={diff.removedEdges} />
          <DiffSection title="Changed edges" values={diff.changedEdges} />
          {diff.policyChanged ? (
            <p className="text-xs text-warning">Workflow policy changed.</p>
          ) : null}
          {diff.inputSchemaChanged ? (
            <p className="text-xs text-warning">Workflow input schema changed.</p>
          ) : null}
          {diff.addedNodes.length +
            diff.removedNodes.length +
            diff.changedNodes.length +
            diff.addedEdges.length +
            diff.removedEdges.length +
            diff.changedEdges.length ===
            0 &&
          !diff.policyChanged &&
          !diff.inputSchemaChanged ? (
            <p className="text-xs text-muted-foreground">
              The two versions are structurally identical.
            </p>
          ) : null}
        </div>
      )}
    </Modal>
  );
}

function DiffSection({ title, values }: { title: string; values: string[] }) {
  if (values.length === 0) return null;
  return (
    <section>
      <h3 className="mb-1 text-xs font-medium">{title}</h3>
      <ul className="flex flex-wrap gap-1">
        {values.map((value) => (
          <li
            key={value}
            className="rounded border border-border px-1.5 py-0.5 font-mono text-[10px]"
          >
            {value}
          </li>
        ))}
      </ul>
    </section>
  );
}
