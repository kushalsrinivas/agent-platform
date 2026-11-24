"use client";

import { KeyRound, Server, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { Select } from "@/components/ui/select";
import { useEnvironments, useMeta, useWorkspaceScope } from "@/lib/api/hooks";
import { useWorkspace } from "@/stores/workspace";

export default function SettingsPage() {
  const { orgId, projectId } = useWorkspaceScope();
  const environmentId = useWorkspace((state) => state.environmentId);
  const setEnvironmentId = useWorkspace((state) => state.setEnvironmentId);
  const openConnection = useWorkspace((state) => state.openConnection);
  const apiKey = useWorkspace((state) => state.apiKey);
  const environments = useEnvironments(orgId, projectId);
  const meta = useMeta();
  const [revealed, setRevealed] = useState(false);

  return (
    <>
      <PageHeader
        title="Settings"
        description="Dashboard scope, credentials and platform build. Configuration lives on the server; the browser stores only the selected scope and an optional API key."
      />

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <KeyRound className="size-4" /> Connection
            </CardTitle>
            <CardDescription>
              The key is sent to the same-origin proxy as{" "}
              <span className="font-mono">X-Dashboard-Api-Key</span> and exchanged for the platform
              API. It is kept in memory for this tab only and is lost on reload.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <span>Credential</span>
              <span className="flex items-center gap-2">
                <span className="font-mono text-xs">
                  {apiKey ? (revealed ? apiKey : `${apiKey.slice(0, 10)}…`) : "server credentials"}
                </span>
                {apiKey ? (
                  <Button size="sm" variant="ghost" onClick={() => setRevealed((value) => !value)}>
                    {revealed ? "Hide" : "Reveal"}
                  </Button>
                ) : null}
              </span>
            </div>
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <span>Organization</span>
              <span className="max-w-[14rem] truncate font-mono text-xs">{orgId || "—"}</span>
            </div>
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <span>Project</span>
              <span className="max-w-[14rem] truncate font-mono text-xs">{projectId || "—"}</span>
            </div>
            <Button variant="outline" size="sm" onClick={openConnection}>
              <KeyRound className="size-3.5" /> Change API key
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Server className="size-4" /> Environment
            </CardTitle>
            <CardDescription>
              The environment selector scopes dashboard context. Executions currently target the
              project; the environment is recorded for operator context.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <Select
              aria-label="Dashboard environment"
              value={environmentId}
              onChange={(event) => setEnvironmentId(event.target.value)}
              className="w-full"
            >
              <option value="">No environment</option>
              {(environments.data?.items ?? []).map((environment) => (
                <option key={environment.id} value={environment.id}>
                  {environment.name} · {environment.kind}
                </option>
              ))}
            </Select>
            <div className="rounded-md border border-border p-3 text-xs text-muted-foreground">
              Project-scoped API keys are recommended for day-to-day use. Organization keys can read
              every project in the organization and should be reserved for administration.
            </div>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="size-4" /> Platform
          </CardTitle>
          <CardDescription>Build identity reported by the API.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{meta.data?.environment ?? "unknown environment"}</Badge>
            <Badge variant="outline">version {meta.data?.version ?? "—"}</Badge>
            <Badge variant="outline" className="font-mono">
              {(meta.data?.commit ?? "unknown").slice(0, 12)}
            </Badge>
          </div>
          <CodeBlock
            label="api.sh"
            code={`curl -s "$PLATFORM_URL/api/v1/meta" \\
  -H "X-API-Key: $AGENT_PLATFORM_API_KEY"`}
            maxHeight="8rem"
          />
          <p className="text-xs text-muted-foreground">
            The dashboard proxy forwards the API key per request. Set{" "}
            <span className="font-mono">DASHBOARD_API_KEY</span> on the dashboard service to use
            server credentials instead.
          </p>
        </CardContent>
      </Card>
    </>
  );
}
