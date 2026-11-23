"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { toast } from "@/components/ui/toast";
import { demoWorkflows } from "@/lib/demo-data";
import { newExecutionSchema } from "@/lib/schemas";

export function NewExecutionDialog() {
  const [open, setOpen] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [workflow, setWorkflow] = useState(demoWorkflows[0]?.name ?? "");
  const [input, setInput] = useState('{\n  "topic": "Durable execution patterns"\n}');

  function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const parsed = newExecutionSchema.safeParse({ workflow, input });
    if (!parsed.success) {
      const fieldErrors: Record<string, string> = {};
      for (const issue of parsed.error.issues) {
        fieldErrors[String(issue.path[0])] = issue.message;
      }
      setErrors(fieldErrors);
      return;
    }

    setErrors({});
    setOpen(false);
    toast.info("Input validated", {
      description: "Execution submission is wired to the API in Phase 04.",
    });
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>New execution</Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="New execution"
        description="Start a published workflow version with immutable JSON input."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" form="new-execution-form">
              Validate input
            </Button>
          </>
        }
      >
        <form id="new-execution-form" onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <label htmlFor="workflow" className="text-sm font-medium">
              Workflow
            </label>
            <select
              id="workflow"
              value={workflow}
              onChange={(event) => setWorkflow(event.target.value)}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              {demoWorkflows
                .filter((item) => item.status === "published")
                .map((item) => (
                  <option key={item.id} value={item.name}>
                    {item.name} · v{item.version}
                  </option>
                ))}
            </select>
            {errors.workflow ? <p className="text-xs text-danger">{errors.workflow}</p> : null}
          </div>

          <div className="space-y-1.5">
            <label htmlFor="input" className="text-sm font-medium">
              Input (JSON object)
            </label>
            <textarea
              id="input"
              value={input}
              onChange={(event) => setInput(event.target.value)}
              rows={8}
              spellCheck={false}
              className="scrollbar-thin w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
            />
            {errors.input ? <p className="text-xs text-danger">{errors.input}</p> : null}
          </div>

          <p className="text-xs text-muted-foreground">
            Inputs are immutable once an execution is accepted. Maximum size is 256 KiB.
          </p>
        </form>
      </Modal>
    </>
  );
}
