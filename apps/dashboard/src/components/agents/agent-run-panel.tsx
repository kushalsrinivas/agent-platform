"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Send } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ApiError, api, apiPaths, newIdempotencyKey } from "@/lib/api";
import {
  useExecutionOutputs,
  useExecutions,
  useWorkflows,
  useWorkspaceScope,
} from "@/lib/api/hooks";
import type { Agent, AgentVersion, Execution, Workflow } from "@/lib/api/types";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

type Turn = { id: string; role: "user" | "assistant"; text: string };

type StoredConversation = { turns: Turn[]; pendingExecutionId: string | null };

function reportFrom(output: unknown): string {
  if (!output || typeof output !== "object") return "";
  const report = (output as { report?: unknown }).report;
  return typeof report === "string" ? report : "";
}

function loadConversation(key: string): StoredConversation | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { turns?: unknown; pendingExecutionId?: unknown };
    if (!Array.isArray(parsed.turns)) return null;
    const turns = parsed.turns.filter((turn): turn is Turn => {
      if (!turn || typeof turn !== "object") return false;
      const value = turn as { id?: unknown; role?: unknown; text?: unknown };
      return (
        typeof value.id === "string" &&
        (value.role === "user" || value.role === "assistant") &&
        typeof value.text === "string"
      );
    });
    return {
      turns,
      pendingExecutionId:
        typeof parsed.pendingExecutionId === "string" ? parsed.pendingExecutionId : null,
    };
  } catch {
    return null;
  }
}

function saveConversation(key: string, value: StoredConversation): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // Storage can be disabled; the conversation still works for this page visit.
  }
}

export function AgentRunPanel({ agent, version }: { agent: Agent; version: AgentVersion }) {
  const { orgId, projectId } = useWorkspaceScope();
  const queryClient = useQueryClient();
  const storageKey = `agent-run:${orgId}:${projectId}:${agent.id}:v${version.version}`;
  const [input, setInput] = useState(() =>
    version.spec.tools?.some((tool) => tool.tool_id === "tool_news_fetch")
      ? "Summarise the latest UK news."
      : "",
  );
  const [turns, setTurns] = useState<Turn[]>([]);
  const [pendingExecutionId, setPendingExecutionId] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);
  const [hasStoredConversation, setHasStoredConversation] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const workflows = useWorkflows(orgId, projectId);
  const runWorkflowName = `agent-run-${agent.id}-v${version.version}`;
  const runWorkflow = useMemo(
    () => (workflows.data?.items ?? []).find((item) => item.name === runWorkflowName),
    [workflows.data, runWorkflowName],
  );
  const executions = useExecutions(orgId, projectId, {
    workflowId: runWorkflow?.id,
    state: "succeeded",
    limit: 1,
  });
  const latest = executions.data?.items[0];
  const outputs = useExecutionOutputs(
    orgId,
    projectId,
    latest?.state === "succeeded" ? latest.id : "",
  );
  const busy = submitting || Boolean(pendingExecutionId);

  useEffect(() => {
    const stored = loadConversation(storageKey);
    if (stored) {
      setTurns(stored.turns);
      setPendingExecutionId(stored.pendingExecutionId);
      setHasStoredConversation(true);
    }
    setHydrated(true);
  }, [storageKey]);

  useEffect(() => {
    if (!hydrated) return;
    saveConversation(storageKey, { turns, pendingExecutionId });
  }, [hydrated, pendingExecutionId, storageKey, turns]);

  useEffect(() => {
    if (
      !hydrated ||
      hasStoredConversation ||
      pendingExecutionId ||
      turns.length > 0 ||
      !runWorkflow ||
      latest?.workflow_id !== runWorkflow.id
    ) {
      return;
    }
    if (latest?.state !== "succeeded") return;
    const report = reportFrom(outputs.data?.agent);
    if (!report) return;
    setTurns([{ id: crypto.randomUUID(), role: "assistant", text: report }]);
  }, [
    hasStoredConversation,
    hydrated,
    latest,
    outputs.data,
    pendingExecutionId,
    runWorkflow,
    turns.length,
  ]);

  useEffect(() => {
    if (!hydrated || !pendingExecutionId) return;
    let cancelled = false;
    let timer: number | undefined;

    async function poll() {
      try {
        const execution = await api<Execution>(
          apiPaths.execution(orgId, projectId, pendingExecutionId as string),
        );
        if (cancelled) return;
        if (!TERMINAL.has(execution.state)) {
          timer = window.setTimeout(poll, 2000);
          return;
        }
        let text = "";
        if (execution.state === "succeeded") {
          const replay = await api<{ outputs?: Record<string, unknown> }>(
            apiPaths.replay(orgId, projectId, pendingExecutionId as string),
          );
          if (cancelled) return;
          text = reportFrom(replay.outputs?.agent) || "The run succeeded but returned no text.";
        } else {
          text = `Run ${execution.state}: ${execution.failure_detail ?? execution.failure_code ?? "no detail"}`;
        }
        setTurns((current) => [...current, { id: crypto.randomUUID(), role: "assistant", text }]);
        setPendingExecutionId(null);
        await queryClient.invalidateQueries({ queryKey: ["executions"] });
      } catch (caught) {
        if (cancelled) return;
        if (caught instanceof ApiError && caught.status === 404) {
          setError("The run could not be found. It may have been deleted.");
          setPendingExecutionId(null);
          return;
        }
        setError(caught instanceof Error ? caught.message : "Could not read the run; retrying…");
        // Keep the execution id: the run may still be alive, and a later page visit can resume it.
        timer = window.setTimeout(poll, 5000);
      }
    }

    void poll();
    return () => {
      cancelled = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [hydrated, orgId, pendingExecutionId, projectId, queryClient]);

  async function ensureWorkflow(): Promise<Workflow> {
    if (runWorkflow) {
      if (runWorkflow.deployed_version) return runWorkflow;
      await api(
        `${apiPaths.workflowVersion(orgId, projectId, runWorkflow.id, runWorkflow.latest_version)}:publish`,
        { method: "POST" },
      );
      await queryClient.invalidateQueries({ queryKey: ["workflows"] });
      return { ...runWorkflow, deployed_version: runWorkflow.latest_version };
    }
    const graph = {
      nodes: [
        {
          id: "agent",
          type: "agent",
          agent: { agent_id: agent.id, version: version.version },
          worker_pool: "default",
          timeout_seconds: 300,
        },
      ],
      edges: [],
      input_schema: { type: "object", properties: { message: { type: "string" } } },
    };
    const created = await api<Workflow>(apiPaths.workflows(orgId, projectId), {
      method: "POST",
      json: {
        name: runWorkflowName,
        description: `Interactive runs for ${agent.name} v${version.version}`,
        version: { graph },
      },
    });
    await api(
      `${apiPaths.workflowVersion(orgId, projectId, created.id, created.latest_version)}:publish`,
      { method: "POST" },
    );
    await queryClient.invalidateQueries({ queryKey: ["workflows"] });
    return created;
  }

  async function run(event: React.FormEvent) {
    event.preventDefault();
    const message = input.trim();
    if (!message || busy) return;
    setSubmitting(true);
    setError(null);
    setInput("");
    const userTurn: Turn = { id: crypto.randomUUID(), role: "user", text: message };
    const history: Turn[] = [...turns, userTurn];
    setTurns(history);
    try {
      const workflow = await ensureWorkflow();
      let transcript = history
        .map((turn) => `${turn.role === "user" ? "User" : "Assistant"}: ${turn.text}`)
        .join("\n\n");
      // Keep the execution input bounded; the agent only needs recent context.
      if (transcript.length > 20_000) transcript = transcript.slice(-20_000);
      const execution = await api<Execution>(apiPaths.executions(orgId, projectId), {
        method: "POST",
        json: {
          workflow_id: workflow.id,
          workflow_version: workflow.deployed_version ?? workflow.latest_version,
          input: { message: transcript },
        },
        idempotencyKey: newIdempotencyKey("agent-run"),
      });
      // Persist before the next render so navigating away immediately still resumes the run.
      saveConversation(storageKey, { turns: history, pendingExecutionId: execution.id });
      setPendingExecutionId(execution.id);
      await queryClient.invalidateQueries({ queryKey: ["executions"] });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not run the agent.");
    } finally {
      setSubmitting(false);
    }
  }

  function reset() {
    if (busy) return;
    setTurns([]);
    setPendingExecutionId(null);
    setError(null);
    saveConversation(storageKey, { turns: [], pendingExecutionId: null });
  }

  const disabled = busy || version.status !== "published" || workflows.isPending;

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div className="space-y-1">
          <CardTitle>Run this agent</CardTitle>
          <CardDescription>
            Ask a question or give it a task. The run continues if you navigate away; come back here
            to pick up the answer.
          </CardDescription>
        </div>
        <Button variant="ghost" size="sm" onClick={reset} disabled={busy || turns.length === 0}>
          New
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="max-h-96 space-y-3 overflow-y-auto rounded-md border border-border bg-muted/20 p-3">
          {turns.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No messages yet. Start with a task or question.
            </p>
          ) : (
            turns.map((turn) => (
              <div
                key={turn.id}
                className={
                  turn.role === "user"
                    ? "ml-8 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground"
                    : "mr-8 whitespace-pre-wrap rounded-md border border-border bg-background px-3 py-2 text-sm leading-6"
                }
              >
                {turn.text}
              </div>
            ))
          )}
          {busy ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> Working…
            </p>
          ) : null}
        </div>

        <form onSubmit={run} className="flex items-end gap-2">
          <label htmlFor="agent-run-input" className="sr-only">
            Message
          </label>
          <textarea
            id="agent-run-input"
            value={input}
            onChange={(event) => setInput(event.target.value)}
            rows={2}
            placeholder="Ask the agent to do something…"
            className="min-h-16 flex-1 resize-y rounded-md border border-input bg-background p-3 text-sm"
          />
          <Button type="submit" disabled={disabled || !input.trim()}>
            <Send className="size-4" /> Run
          </Button>
        </form>

        {version.status !== "published" ? (
          <p className="text-xs text-muted-foreground">Publish this version before running it.</p>
        ) : null}
        {error ? <p className="text-xs text-danger">{error}</p> : null}
      </CardContent>
    </Card>
  );
}
