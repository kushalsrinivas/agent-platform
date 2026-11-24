import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { apiPaths } from "@/lib/api";
import { queryKeys } from "@/lib/api/hooks";
import type { Event, Execution } from "@/lib/api/types";
import { useWorkspace } from "@/stores/workspace";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

export type LiveState = "idle" | "connecting" | "live" | "reconnecting" | "closed" | "error";

/** Follows the persisted SSE stream, merging events into the Query cache and reconnecting with event ids. */
export function useExecutionStream(
  orgId: string,
  projectId: string,
  executionId: string,
  enabled = true,
) {
  const queryClient = useQueryClient();
  const [state, setState] = useState<LiveState>("idle");
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!enabled || !orgId || !projectId || !executionId) return;
    let closed = false;
    let after = 0;
    let controller: AbortController | null = null;
    let invalidateTimer: ReturnType<typeof setTimeout> | null = null;

    const invalidate = () => {
      if (invalidateTimer) return;
      invalidateTimer = setTimeout(() => {
        invalidateTimer = null;
        void queryClient.invalidateQueries({
          queryKey: queryKeys.execution(orgId, projectId, executionId),
        });
        void queryClient.invalidateQueries({
          queryKey: queryKeys.tasks(orgId, projectId, executionId),
        });
        void queryClient.invalidateQueries({
          queryKey: queryKeys.outputs(orgId, projectId, executionId),
        });
        void queryClient.invalidateQueries({
          queryKey: queryKeys.executionApprovals(orgId, projectId, executionId),
        });
      }, 350);
    };

    async function connect() {
      if (closed) return;
      setState((current) => (current === "live" ? "reconnecting" : "connecting"));
      controller = new AbortController();
      try {
        const key = useWorkspace.getState().apiKey;
        const response = await fetch(
          `/api/platform/${apiPaths.stream(orgId, projectId, executionId, after)}`,
          {
            headers: {
              Accept: "text/event-stream",
              ...(key ? { "X-Dashboard-Api-Key": key } : {}),
            },
            signal: controller.signal,
            cache: "no-store",
          },
        );
        if (!response.ok) {
          throw new Error(`stream ${response.status}`);
        }
        setState("live");
        setError(null);
        const reader = response.body?.getReader();
        if (!reader) throw new Error("stream body is unavailable");
        const decoder = new TextDecoder();
        let buffer = "";
        while (!closed) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const blocks = buffer.split("\n\n");
          buffer = blocks.pop() ?? "";
          for (const block of blocks) {
            const data = block
              .split("\n")
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trim())
              .join("\n");
            if (!data) continue;
            try {
              const event = JSON.parse(data) as Event;
              if (typeof event.seq === "number" && event.seq > after) after = event.seq;
              queryClient.setQueryData<Event[]>(
                queryKeys.events(orgId, projectId, executionId),
                (current = []) => {
                  if (current.some((item) => item.seq === event.seq)) return current;
                  return [...current, event].sort((a, b) => a.seq - b.seq);
                },
              );
              invalidate();
            } catch {
              // Ignore malformed event frames; the next reconnect re-reads persisted history.
            }
          }
        }
        if (closed) return;
        const execution = queryClient.getQueryData<Execution>(
          queryKeys.execution(orgId, projectId, executionId),
        );
        if (execution && TERMINAL.has(execution.state)) {
          setState("closed");
          return;
        }
        setState("reconnecting");
        timer.current = setTimeout(connect, 1200);
      } catch (caught) {
        if (closed || controller?.signal.aborted) return;
        setError(caught instanceof Error ? caught.message : "stream failed");
        setState("reconnecting");
        timer.current = setTimeout(connect, 2000);
      }
    }

    void connect();
    return () => {
      closed = true;
      controller?.abort();
      if (timer.current) clearTimeout(timer.current);
      if (invalidateTimer) clearTimeout(invalidateTimer);
      setState("idle");
    };
  }, [enabled, orgId, projectId, executionId, queryClient]);

  return { state, error };
}
