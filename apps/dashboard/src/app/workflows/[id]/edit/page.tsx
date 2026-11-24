"use client";

import { use } from "react";

import { WorkflowBuilder } from "@/components/builder/workflow-builder";

export default function EditWorkflowPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <WorkflowBuilder workflowId={id} />;
}
