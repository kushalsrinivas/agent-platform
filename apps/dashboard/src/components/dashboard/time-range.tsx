"use client";

import { useState } from "react";

import { cn } from "@/lib/utils";

export type TimeRange = { key: string; from: string; to: string };

const PRESETS = [
  { key: "1h", label: "1h", hours: 1 },
  { key: "24h", label: "24h", hours: 24 },
  { key: "7d", label: "7d", hours: 24 * 7 },
  { key: "30d", label: "30d", hours: 24 * 30 },
] as const;

export function defaultRange(key = "24h"): TimeRange {
  const preset = PRESETS.find((item) => item.key === key) ?? PRESETS[1];
  const to = new Date();
  const from = new Date(to.getTime() - preset.hours * 3_600_000);
  return { key: preset.key, from: from.toISOString(), to: to.toISOString() };
}

export function TimeRangePicker({
  value,
  onChange,
}: {
  value: TimeRange;
  onChange: (value: TimeRange) => void;
}) {
  const [customOpen, setCustomOpen] = useState(value.key === "custom");
  const [from, setFrom] = useState(value.from.slice(0, 16));
  const [to, setTo] = useState(value.to.slice(0, 16));

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {PRESETS.map((preset) => (
        <button
          key={preset.key}
          type="button"
          onClick={() => {
            setCustomOpen(false);
            onChange(defaultRange(preset.key));
          }}
          className={cn(
            "h-8 rounded-md border px-2.5 text-xs transition-colors",
            value.key === preset.key
              ? "border-primary/30 bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          {preset.label}
        </button>
      ))}
      <button
        type="button"
        onClick={() => setCustomOpen((open) => !open)}
        className={cn(
          "h-8 rounded-md border px-2.5 text-xs transition-colors",
          value.key === "custom"
            ? "border-primary/30 bg-primary/10 text-primary"
            : "border-border text-muted-foreground hover:bg-muted hover:text-foreground",
        )}
      >
        Custom
      </button>
      {customOpen ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <input
            type="datetime-local"
            value={from}
            onChange={(event) => setFrom(event.target.value)}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
            aria-label="From"
          />
          <span className="text-xs text-muted-foreground">to</span>
          <input
            type="datetime-local"
            value={to}
            onChange={(event) => setTo(event.target.value)}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
            aria-label="To"
          />
          <button
            type="button"
            onClick={() =>
              onChange({
                key: "custom",
                from: new Date(from).toISOString(),
                to: new Date(to).toISOString(),
              })
            }
            className="h-8 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground"
          >
            Apply
          </button>
        </div>
      ) : null}
    </div>
  );
}
