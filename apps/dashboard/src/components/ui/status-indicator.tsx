import { cn } from "@/lib/utils";

export type StatusMeta = {
  label: string;
  dot: string;
  text: string;
  pulse: boolean;
  variant: "neutral" | "success" | "danger" | "warning" | "info";
};

const FALLBACK: StatusMeta = {
  label: "Unknown",
  dot: "bg-muted-foreground",
  text: "text-muted-foreground",
  pulse: false,
  variant: "neutral",
};

export const STATUS: Record<string, StatusMeta> = {
  queued: {
    label: "Queued",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
    variant: "neutral",
  },
  pending: {
    label: "Queued",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
    variant: "neutral",
  },
  assigned: { label: "Assigned", dot: "bg-info", text: "text-info", pulse: false, variant: "info" },
  running: { label: "Running", dot: "bg-info", text: "text-info", pulse: true, variant: "info" },
  succeeded: {
    label: "Succeeded",
    dot: "bg-success",
    text: "text-success",
    pulse: false,
    variant: "success",
  },
  failed: {
    label: "Failed",
    dot: "bg-danger",
    text: "text-danger",
    pulse: false,
    variant: "danger",
  },
  failing: {
    label: "Failing",
    dot: "bg-danger",
    text: "text-danger",
    pulse: true,
    variant: "danger",
  },
  cancelled: {
    label: "Cancelled",
    dot: "bg-warning",
    text: "text-warning",
    pulse: false,
    variant: "warning",
  },
  cancel_requested: {
    label: "Cancelling",
    dot: "bg-warning",
    text: "text-warning",
    pulse: true,
    variant: "warning",
  },
  skipped: {
    label: "Skipped",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
    variant: "neutral",
  },
  draft: {
    label: "Draft",
    dot: "bg-warning",
    text: "text-warning",
    pulse: false,
    variant: "warning",
  },
  published: {
    label: "Published",
    dot: "bg-success",
    text: "text-success",
    pulse: false,
    variant: "success",
  },
  deprecated: {
    label: "Deprecated",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
    variant: "neutral",
  },
  available: {
    label: "Available",
    dot: "bg-success",
    text: "text-success",
    pulse: false,
    variant: "success",
  },
  busy: { label: "Busy", dot: "bg-info", text: "text-info", pulse: true, variant: "info" },
  draining: {
    label: "Draining",
    dot: "bg-warning",
    text: "text-warning",
    pulse: false,
    variant: "warning",
  },
  unhealthy: {
    label: "Unhealthy",
    dot: "bg-danger",
    text: "text-danger",
    pulse: true,
    variant: "danger",
  },
  terminated: {
    label: "Terminated",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
    variant: "neutral",
  },
  approved: {
    label: "Approved",
    dot: "bg-success",
    text: "text-success",
    pulse: false,
    variant: "success",
  },
  rejected: {
    label: "Rejected",
    dot: "bg-danger",
    text: "text-danger",
    pulse: false,
    variant: "danger",
  },
  expired: {
    label: "Expired",
    dot: "bg-warning",
    text: "text-warning",
    pulse: false,
    variant: "warning",
  },
};

export function statusMeta(status: string): StatusMeta {
  return STATUS[status] ?? { ...FALLBACK, label: status || "Unknown" };
}

type StatusIndicatorProps = {
  status: string;
  className?: string;
  showLabel?: boolean;
};

/** The one place status colour and wording are defined. Colour alone is never the only signal. */
export function StatusIndicator({ status, className, showLabel = true }: StatusIndicatorProps) {
  const meta = statusMeta(status);
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
