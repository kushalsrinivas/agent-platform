import type * as React from "react";

import { cn } from "@/lib/utils";

export function Tabs({
  value,
  onValueChange,
  items,
  className,
}: {
  value: string;
  onValueChange: (value: string) => void;
  items: { value: string; label: string; count?: number }[];
  className?: string;
}) {
  return (
    <div
      className={cn("flex flex-nowrap gap-1 overflow-x-auto border-b border-border", className)}
      role="tablist"
    >
      {items.map((item) => {
        const selected = item.value === value;
        return (
          <button
            key={item.value}
            type="button"
            role="tab"
            aria-selected={selected}
            onClick={() => onValueChange(item.value)}
            className={cn(
              "-mb-px shrink-0 border-b-2 px-2.5 py-2 text-xs font-medium transition-colors",
              selected
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {item.label}
            {item.count !== undefined ? (
              <span className="ml-1.5 rounded bg-muted px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground">
                {item.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

export function TabPanel({ children }: { children: React.ReactNode }) {
  return (
    <div role="tabpanel" className="pt-4">
      {children}
    </div>
  );
}
