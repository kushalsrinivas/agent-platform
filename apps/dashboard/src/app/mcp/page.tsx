"use client";

import { Plug } from "lucide-react";
import Link from "next/link";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { EmptyState } from "@/components/ui/empty-state";
import { useMCPServers, useWorkspaceScope } from "@/lib/api/hooks";
import { redactedJson } from "@/lib/redact";

export default function MCPPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const servers = useMCPServers(orgId, projectId);
  const list = servers.data?.items ?? [];

  return (
    <>
      <PageHeader
        title="MCP servers"
        description="Model Context Protocol servers referenced by agent versions. Credentials stay behind a reference name and are never rendered."
      />
      <Card>
        <CardHeader>
          <CardTitle>Server bindings</CardTitle>
          <CardDescription>
            Transport, tool policy and the agents that depend on each server.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {servers.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading MCP servers…</p>
          ) : list.length === 0 ? (
            <EmptyState
              icon={Plug}
              title="No MCP servers"
              description="Add an MCP server to an agent spec, publish a version, and it will appear here."
              className="m-4"
            />
          ) : (
            <ul className="divide-y divide-border">
              {list.map((server) => (
                <li key={server.name} className="space-y-2 px-4 py-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate font-mono text-sm">{server.name}</p>
                      <p className="truncate text-xs text-muted-foreground">
                        {server.url ??
                          [server.command, ...(server.args ?? [])].filter(Boolean).join(" ")}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge variant="outline">{server.transport}</Badge>
                      <Badge variant={server.auth_credential_ref ? "success" : "neutral"}>
                        {server.auth_credential_ref ? "credential ref" : "no auth"}
                      </Badge>
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {server.agents.map((agent) => (
                      <Link
                        key={`${agent.agent_id}-${agent.version}`}
                        href={`/agents/${agent.agent_id}`}
                        className="rounded-md border border-border px-2 py-1 text-[11px] hover:bg-muted"
                      >
                        {agent.agent_name} v{agent.version}
                      </Link>
                    ))}
                    {server.agents.length === 0 ? (
                      <span className="text-[11px] text-muted-foreground">
                        Not referenced by a published agent.
                      </span>
                    ) : null}
                  </div>
                  {Object.keys(server.tool_policies ?? {}).length > 0 ? (
                    <details>
                      <summary className="cursor-pointer text-[11px] text-muted-foreground hover:text-foreground">
                        Tool policies
                      </summary>
                      <div className="mt-1.5">
                        <CodeBlock code={redactedJson(server.tool_policies)} maxHeight="10rem" />
                      </div>
                    </details>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </>
  );
}
