import type { LucideIcon } from "lucide-react";
import { Inbox } from "lucide-react";
import type * as React from "react";

import { cn } from "@/lib/utils";

export function EmptyState({
  icon: Icon = Inbox,
  title,
  description,
  action,
  className,
}: {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-dashed border-border px-6 py-14 text-center",
        className,
      )}
    >
      <span className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">
        <Icon className="size-4" aria-hidden="true" />
      </span>
      <p className="mt-3 text-sm font-medium">{title}</p>
      {description ? (
        <p className="mt-1 max-w-md text-xs leading-relaxed text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
