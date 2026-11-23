"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type CodeBlockProps = {
  code: string;
  label?: string;
  language?: string;
  className?: string;
  maxHeight?: string;
};

export function CodeBlock({
  code,
  label,
  language = "json",
  className,
  maxHeight = "20rem",
}: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      toast.success("Copied to clipboard");
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Could not access the clipboard");
    }
  }

  return (
    <div className={cn("overflow-hidden rounded-lg border border-border bg-muted/40", className)}>
      <div className="flex items-center justify-between border-b border-border px-3 py-1.5">
        <span className="font-mono text-[11px] uppercase tracking-wide text-muted-foreground">
          {label ?? language}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-muted-foreground"
          onClick={copy}
          aria-label="Copy code"
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <pre
        className="scrollbar-thin overflow-auto p-3 text-xs leading-relaxed"
        style={{ maxHeight }}
      >
        <code className="font-mono">{code}</code>
      </pre>
    </div>
  );
}
