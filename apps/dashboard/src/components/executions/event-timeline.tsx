"use client";

import { CheckCircle2, CircleDot, CircleX, Play, RotateCcw } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { CodeBlock } from "@/components/ui/code-block";
import { EmptyState } from "@/components/ui/empty-state";
import type { Event } from "@/lib/api/types";
import { redactedJson } from "@/lib/redact";
import { cn, formatDateTime } from "@/lib/utils";

function iconFor(event: Event) {
  if (event.to_state === "failed" || event.event_type.includes("failed")) return CircleX;
  if (event.to_state === "succeeded" || event.event_type.includes("succeeded")) return CheckCircle2;
  if (event.event_type.includes("started") || event.event_type.includes("created")) return Play;
  if (event.event_type.includes("attempt")) return RotateCcw;
  return CircleDot;
}

export function EventTimeline({
  events,
  onSelectEntity,
  dense = false,
}: {
  events: Event[];
  onSelectEntity?: (entityId: string) => void;
  dense?: boolean;
}) {
  if (events.length === 0) {
    return (
      <EmptyState title="No events" description="The execution has not emitted any events yet." />
    );
  }
  return (
    <ol className={cn("border-l border-border", dense ? "space-y-1 pl-4" : "space-y-4 pl-5")}>
      {events.map((event) => {
        const Icon = iconFor(event);
        const failed = event.to_state === "failed";
        return (
          <li key={event.seq} className="relative">
            <span
              className={cn(
                "absolute rounded-full border-2 border-background",
                dense
                  ? "-left-[1.2rem] top-1 grid size-3.5 place-items-center"
                  : "-left-[1.45rem] top-1.5 grid size-4 place-items-center",
                failed ? "bg-danger/15 text-danger" : "bg-primary/10 text-primary",
              )}
            >
              <Icon className={dense ? "size-2" : "size-2.5"} aria-hidden="true" />
            </span>
            <div className={cn("flex flex-wrap items-center gap-2", dense && "text-xs")}>
              <button
                type="button"
                className="font-mono text-xs hover:underline"
                onClick={() => onSelectEntity?.(event.entity_id)}
              >
                {event.event_type}
              </button>
              {event.to_state ? (
                <Badge variant={failed ? "danger" : "outline"}>{event.to_state}</Badge>
              ) : null}
              <span className="text-[11px] text-muted-foreground">
                {formatDateTime(event.created_at)}
              </span>
            </div>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {event.entity_type} {event.entity_id}
              {event.reason ? ` · ${event.reason}` : ""} · {event.actor_type} {event.actor_id}
            </p>
            {!dense && Object.keys(event.payload ?? {}).length > 0 ? (
              <details className="mt-1">
                <summary className="cursor-pointer text-[11px] text-muted-foreground hover:text-foreground">
                  Payload
                </summary>
                <div className="mt-1.5">
                  <CodeBlock code={redactedJson(event.payload)} maxHeight="10rem" />
                </div>
              </details>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
