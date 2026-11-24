"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { toast } from "@/components/ui/toast";
import { api, apiPaths, newIdempotencyKey } from "@/lib/api";
import { useWorkspaceScope } from "@/lib/api/hooks";
import type { Agent, AgentVersion, Execution, Workflow } from "@/lib/api/types";

export function AgentTestDialog({ agent, version }: { agent: Agent; version: AgentVersion }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const { orgId, projectId } = useWorkspaceScope();
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState('{\n  "topic": "What can you help me with?"\n}');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function runTest(event: React.FormEvent) {
    event.preventDefault();
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
      const graph = {
        nodes: [
          { id: "start", type: "start" },
          {
            id: "agent",
            type: "agent",
            agent: { agent_id: agent.id, version: version.version },
            worker_pool: "default",
            timeout_seconds: 300,
          },
          { id: "end", type: "end" },
        ],
        edges: [
          { from: "start", to: "agent" },
          { from: "agent", to: "end" },
        ],
        input_schema: { type: "object" },
      };
      const workflow = await api<Workflow>(apiPaths.workflows(orgId, projectId), {
        method: "POST",
        json: {
          name: `agent-test-${agent.name}-${Date.now()}`,
          description: `Test run for ${agent.name} v${version.version}`,
          version: { graph },
        },
      });
      await api(`${apiPaths.workflowVersion(orgId, projectId, workflow.id, 1)}:publish`, {
        method: "POST",
      });
      const execution = await api<Execution>(apiPaths.executions(orgId, projectId), {
        method: "POST",
        json: {
          workflow_id: workflow.id,
          workflow_version: 1,
          input: parsed,
        },
        idempotencyKey: newIdempotencyKey("agent-test"),
      });
      await queryClient.invalidateQueries({ queryKey: ["executions"] });
      toast.success("Test execution started");
      setOpen(false);
      router.push(`/executions/${execution.id}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not start the test execution.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)} disabled={version.status !== "published"}>
        JSON test
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={`Test ${agent.name}`}
        description="Runs a temporary one-node workflow pinned to the selected immutable version."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" form="agent-test-form" disabled={submitting}>
              {submitting ? "Starting…" : "Run test"}
            </Button>
          </>
        }
      >
        <form id="agent-test-form" onSubmit={runTest} className="space-y-3">
          <label htmlFor="agent-test-input" className="text-sm font-medium">
            Input (JSON object)
          </label>
          <textarea
            id="agent-test-input"
            value={input}
            onChange={(event) => setInput(event.target.value)}
            rows={8}
            spellCheck={false}
            className="w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
          />
          {error ? <p className="text-xs text-danger">{error}</p> : null}
          <p className="text-xs text-muted-foreground">
            The test workflow is stored in the project and can be inspected from Workflows.
          </p>
        </form>
      </Modal>
    </>
  );
}
