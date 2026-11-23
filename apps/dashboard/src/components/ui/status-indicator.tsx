import type { ExecutionStatus } from "@/lib/schemas";
import { cn } from "@/lib/utils";

const STATUS: Record<
  ExecutionStatus,
  { label: string; dot: string; text: string; pulse: boolean }
> = {
  queued: {
    label: "Queued",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
  },
  running: { label: "Running", dot: "bg-info", text: "text-info", pulse: true },
  succeeded: { label: "Succeeded", dot: "bg-success", text: "text-success", pulse: false },
  failed: { label: "Failed", dot: "bg-danger", text: "text-danger", pulse: false },
  cancelled: { label: "Cancelled", dot: "bg-warning", text: "text-warning", pulse: false },
};

type StatusIndicatorProps = {
  status: ExecutionStatus;
  className?: string;
  showLabel?: boolean;
};

/** The one place status colour and wording are defined. Colour alone is never the only signal. */
export function StatusIndicator({ status, className, showLabel = true }: StatusIndicatorProps) {
  const meta = STATUS[status];
  return (
    <span
      className={cn("inline-flex items-center gap-2 text-xs font-medium", meta.text, className)}
    >
      <span className="relative flex size-2" aria-hidden="true">
        {meta.pulse ? (
          <span
            className={cn(
              "absolute inline-flex size-full animate-ping rounded-full opacity-60",
              meta.dot,
            )}
          />
        ) : null}
        <span className={cn("relative inline-flex size-2 rounded-full", meta.dot)} />
      </span>
      {showLabel ? meta.label : <span className="sr-only">Status: {meta.label}</span>}
    </span>
  );
}

export { STATUS as executionStatusMeta };
