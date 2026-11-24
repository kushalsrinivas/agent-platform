"use client";

import { AlertTriangle, RotateCcw, XCircle } from "lucide-react";

import { EventTimeline } from "@/components/executions/event-timeline";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Event, Execution, Task, TaskDetail } from "@/lib/api/types";
import { formatDateTime, formatDuration } from "@/lib/utils";

export function FailureInspector({
  execution,
  tasks,
  events,
  selectedTask,
  selectedTaskId,
  onSelectTask,
  onResume,
  onCancel,
  resuming,
}: {
  execution: Execution;
  tasks: Task[];
  events: Event[];
  selectedTask: TaskDetail | undefined;
  selectedTaskId: string | null;
  onSelectTask: (taskId: string) => void;
  onResume: () => void;
  onCancel: () => void;
  resuming: boolean;
}) {
  const failed = tasks.filter((task) => task.state === "failed");
  const cancelable = ["pending", "assigned", "running", "failing"].includes(execution.state);
  const resumable = ["failed", "cancelled"].includes(execution.state);

  return (
    <div className="space-y-6">
      <Card className="border-danger/30 bg-danger/5">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-danger">
            <AlertTriangle className="size-4" />
            {execution.state === "failed" ? "Execution failed" : `Execution ${execution.state}`}
          </CardTitle>
          <CardDescription>
            {execution.failure_code ?? "No top-level failure code"}
            {execution.failure_detail ? ` · ${execution.failure_detail}` : ""}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-2">
          {resumable ? (
            <Button size="sm" onClick={onResume} disabled={resuming}>
              <RotateCcw className="size-3.5" /> {resuming ? "Resuming…" : "Resume execution"}
            </Button>
          ) : null}
          {cancelable ? (
            <Button size="sm" variant="outline" onClick={onCancel}>
              <XCircle className="size-3.5" /> Cancel execution
            </Button>
          ) : null}
          <Badge variant="outline">attempt history below</Badge>
        </CardContent>
      </Card>

      {failed.length === 0 ? (
        <EmptyState
          icon={AlertTriangle}
          title="No failed task"
          description="The execution did not fail at a node, or the failure was outside the task path."
        />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Failed nodes</CardTitle>
            <CardDescription>
              Select a node to inspect its attempts and error details.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {failed.map((task) => (
              <button
                key={task.id}
                type="button"
                onClick={() => onSelectTask(task.id)}
                className={`flex w-full items-center justify-between gap-3 rounded-md border px-3 py-2 text-left text-sm ${
                  selectedTaskId === task.id
                    ? "border-primary/50 bg-primary/5"
                    : "border-border hover:bg-muted"
                }`}
              >
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs">{task.node_id}</span>
                  <span className="block text-[11px] text-muted-foreground">
                    {task.skip_reason ?? task.node_type} · {task.attempt_count}/{task.max_attempts}{" "}
                    attempts
                  </span>
                </span>
                <StatusIndicator status={task.state} />
              </button>
            ))}
          </CardContent>
        </Card>
      )}

      {selectedTask ? (
        <Card>
          <CardHeader>
            <CardTitle>Retry history · {selectedTask.node_id}</CardTitle>
            <CardDescription>Errors and worker assignment for each attempt.</CardDescription>
          </CardHeader>
          <CardContent className="px-2">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead>Worker</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead>Duration</TableHead>
                  <TableHead>Error</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {selectedTask.attempts.map((attempt) => {
                  const duration =
                    attempt.started_at && attempt.finished_at
                      ? new Date(attempt.finished_at).getTime() -
                        new Date(attempt.started_at).getTime()
                      : null;
                  return (
                    <TableRow key={attempt.id}>
                      <TableCell className="font-mono text-xs">{attempt.attempt_number}</TableCell>
                      <TableCell>
                        <StatusIndicator status={attempt.outcome ?? "running"} />
                      </TableCell>
                      <TableCell className="max-w-[10rem] truncate font-mono text-[10px]">
                        {attempt.worker_id}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDateTime(attempt.dispatched_at)}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {duration === null ? "—" : formatDuration(duration)}
                      </TableCell>
                      <TableCell className="max-w-[16rem] text-xs">
                        <span className="font-mono text-danger">{attempt.error_code ?? "—"}</span>
                        {attempt.error_message ? (
                          <span
                            className="mt-0.5 block truncate text-muted-foreground"
                            title={attempt.error_message}
                          >
                            {attempt.error_message}
                          </span>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
            <p className="px-3 pb-3 pt-1 text-[11px] text-muted-foreground">
              Stack traces and raw runtime logs are not persisted by the platform. The error code,
              message, event history and output are the durable diagnostic record.
            </p>
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Related events</CardTitle>
          <CardDescription>Failure and retry timeline for this execution.</CardDescription>
        </CardHeader>
        <CardContent>
          <EventTimeline
            events={[...events]
              .filter(
                (event) =>
                  event.to_state === "failed" ||
                  event.event_type.includes("failed") ||
                  event.event_type.includes("attempt") ||
                  event.event_type.includes("execution"),
              )
              .slice(-30)}
            dense
          />
        </CardContent>
      </Card>
    </div>
  );
}
