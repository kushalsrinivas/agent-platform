"use client";

import {
  Bot,
  Braces,
  CircleStop,
  Clock3,
  GitBranch,
  Merge,
  Play,
  Repeat,
  Search,
  ShieldCheck,
  Split,
  Workflow,
  Wrench,
} from "lucide-react";
import { useMemo, useState } from "react";

import { Input } from "@/components/ui/input";
import type { WorkflowNode } from "@/lib/api/types";
import { defaultNode } from "@/lib/builder/graph";
import { cn } from "@/lib/utils";

type LibraryItem = {
  type: string;
  label: string;
  description: string;
  icon: typeof Bot;
};

const CATEGORIES: { name: string; items: LibraryItem[] }[] = [
  {
    name: "Core",
    items: [
      { type: "start", label: "Start", description: "Entry point of the workflow.", icon: Play },
      {
        type: "end",
        label: "End",
        description: "Terminates every active branch.",
        icon: CircleStop,
      },
      { type: "agent", label: "Agent", description: "Run a pinned agent version.", icon: Bot },
      {
        type: "llm",
        label: "LLM",
        description: "Run an agent configured as a single LLM step.",
        icon: Bot,
      },
      { type: "tool", label: "Tool", description: "Call a platform tool version.", icon: Wrench },
    ],
  },
  {
    name: "Logic",
    items: [
      {
        type: "condition",
        label: "Condition",
        description: "Evaluate a CEL expression and branch.",
        icon: GitBranch,
      },
      {
        type: "parallel",
        label: "Parallel",
        description: "Fan-out marker for independent branches.",
        icon: Split,
      },
      {
        type: "join",
        label: "Join",
        description: "Wait for all or a number of branches.",
        icon: Merge,
      },
      {
        type: "loop",
        label: "Loop",
        description: "Run a child workflow up to a bounded count.",
        icon: Repeat,
      },
    ],
  },
  {
    name: "Execution",
    items: [
      {
        type: "delay",
        label: "Delay",
        description: "Durable wait by time or event.",
        icon: Clock3,
      },
      {
        type: "approval",
        label: "Approval",
        description: "Pause for a human decision.",
        icon: ShieldCheck,
      },
      {
        type: "subworkflow",
        label: "Subworkflow",
        description: "Run a pinned child workflow.",
        icon: Workflow,
      },
    ],
  },
  {
    name: "Data",
    items: [
      {
        type: "transform",
        label: "Transform",
        description: "Map upstream outputs with CEL expressions.",
        icon: Braces,
      },
    ],
  },
];

export function NodeLibrary({
  onAdd,
  onDragStart,
}: {
  onAdd: (type: string) => void;
  onDragStart: (event: React.DragEvent, type: string) => void;
}) {
  const [query, setQuery] = useState("");
  const term = query.trim().toLowerCase();
  const categories = useMemo(
    () =>
      CATEGORIES.map((category) => ({
        ...category,
        items: category.items.filter(
          (item) =>
            !term ||
            item.label.toLowerCase().includes(term) ||
            item.type.includes(term) ||
            item.description.toLowerCase().includes(term),
        ),
      })).filter((category) => category.items.length > 0),
    [term],
  );

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border p-3">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-2 size-3.5 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Filter nodes"
            className="h-8 pl-8 text-xs"
            aria-label="Filter node library"
          />
        </div>
      </div>
      <div className="scrollbar-thin flex-1 space-y-4 overflow-y-auto p-3">
        {categories.length === 0 ? (
          <p className="px-1 py-6 text-center text-xs text-muted-foreground">
            No matching node type.
          </p>
        ) : null}
        {categories.map((category) => (
          <section key={category.name}>
            <h3 className="mb-1.5 px-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {category.name}
            </h3>
            <ul className="space-y-1">
              {category.items.map((item) => {
                const Icon = item.icon;
                return (
                  <li key={item.type}>
                    <button
                      type="button"
                      draggable
                      onDragStart={(event) => onDragStart(event, item.type)}
                      onClick={() => onAdd(item.type)}
                      title={item.description}
                      className={cn(
                        "flex w-full items-start gap-2 rounded-md border border-transparent px-2 py-1.5 text-left",
                        "hover:border-border hover:bg-muted/60",
                      )}
                    >
                      <Icon
                        className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                        aria-hidden="true"
                      />
                      <span className="min-w-0">
                        <span className="block text-xs font-medium">{item.label}</span>
                        <span className="block truncate text-[10px] text-muted-foreground">
                          {item.description}
                        </span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
        <p className="rounded-md border border-dashed border-border p-2 text-[10px] leading-relaxed text-muted-foreground">
          Drag onto the canvas or click to append. The graph serializes to the Phase 03 contract;
          switch, merge, filter and validate are expressed as condition, join and transform nodes.
        </p>
      </div>
    </div>
  );
}

export function createNode(type: string, id: string): WorkflowNode {
  return { ...defaultNode(type), id };
}
