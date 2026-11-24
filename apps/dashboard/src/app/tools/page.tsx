"use client";

import { Wrench } from "lucide-react";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { EmptyState } from "@/components/ui/empty-state";
import { useTools, useWorkspaceScope } from "@/lib/api/hooks";
import { redactedJson } from "@/lib/redact";

export default function ToolsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const tools = useTools(orgId, projectId);
  const list = tools.data?.items ?? [];

  return (
    <>
      <PageHeader
        title="Tools"
        description="Tool definitions registered in the project. Agents pin a tool version, and runtime policies decide what the model may call."
      />
      <Card>
        <CardHeader>
          <CardTitle>Tool registry</CardTitle>
          <CardDescription>
            Schemas are shown read-only and redacted before rendering.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {tools.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading tools…</p>
          ) : list.length === 0 ? (
            <EmptyState
              icon={Wrench}
              title="No tools"
              description="Register tools through the SDK or CLI, then bind them to agent versions."
              className="m-4"
            />
          ) : (
            <ul className="divide-y divide-border">
              {list.map((tool) => (
                <li key={tool.tool_id} className="space-y-2 px-4 py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{tool.name}</p>
                      <p className="truncate font-mono text-[11px] text-muted-foreground">
                        {tool.tool_id}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className="font-mono text-[10px]">
                        v{tool.version}
                      </Badge>
                      <Badge variant="neutral">{tool.source}</Badge>
                    </div>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {tool.description || "No description."}
                  </p>
                  <details>
                    <summary className="cursor-pointer text-[11px] text-muted-foreground hover:text-foreground">
                      Input schema
                    </summary>
                    <div className="mt-1.5">
                      <CodeBlock code={redactedJson(tool.schema ?? {})} maxHeight="10rem" />
                    </div>
                  </details>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </>
  );
}
