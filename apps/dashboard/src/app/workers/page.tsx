"use client";

import { Cpu, HardDrive, Users } from "lucide-react";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusIndicator } from "@/components/ui/status-indicator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useWorkers, useWorkspaceScope } from "@/lib/api/hooks";
import { formatRelative } from "@/lib/format";

export default function WorkersPage() {
  const { orgId } = useWorkspaceScope();
  const workers = useWorkers(orgId);
  const list = workers.data?.items ?? [];
  const available = list.reduce((sum, worker) => sum + worker.available_slots, 0);
  const capacity = list.reduce((sum, worker) => sum + worker.max_concurrency, 0);

  return (
    <>
      <PageHeader
        title="Workers"
        description="Live worker registry for the organization. Heartbeats refresh every ten seconds; stale workers stop receiving work."
      />

      <div className="grid gap-3 sm:grid-cols-3">
        <Stat label="Workers" value={String(list.length)} icon={Users} />
        <Stat label="Slots" value={`${available}/${capacity}`} icon={Cpu} />
        <Stat
          label="Unhealthy"
          value={String(list.filter((worker) => worker.status === "unhealthy").length)}
          icon={HardDrive}
        />
      </div>

      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Worker pools</CardTitle>
          <CardDescription>Capacity, runtime and last heartbeat for each worker.</CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          {workers.isPending ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">Loading workers…</p>
          ) : list.length === 0 ? (
            <EmptyState
              icon={Users}
              title="No workers connected"
              description="Start a worker against this organization to run executions."
              className="m-4"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Worker</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Slots</TableHead>
                  <TableHead className="hidden md:table-cell">Runtime</TableHead>
                  <TableHead className="hidden lg:table-cell">Resources</TableHead>
                  <TableHead className="hidden xl:table-cell">Capabilities</TableHead>
                  <TableHead>Heartbeat</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((worker) => (
                  <TableRow key={worker.id}>
                    <TableCell className="max-w-[14rem]">
                      <p className="truncate font-mono text-xs">{worker.id}</p>
                      <p className="truncate text-[11px] text-muted-foreground">
                        pool {worker.pool}
                      </p>
                    </TableCell>
                    <TableCell>
                      <StatusIndicator status={worker.status} />
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      {worker.available_slots}/{worker.max_concurrency}
                      <span className="ml-1 text-muted-foreground">
                        ({worker.reserved_slots} reserved)
                      </span>
                    </TableCell>
                    <TableCell className="hidden text-xs md:table-cell">
                      <p>{worker.runtime}</p>
                      <p className="text-muted-foreground">
                        {worker.runtime_version} · binary {worker.binary_version}
                      </p>
                    </TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">
                      {worker.cpu_cores} cores
                      {worker.cpu_usage_percent !== null
                        ? ` · ${worker.cpu_usage_percent.toFixed(0)}% cpu`
                        : ""}
                      <br />
                      {worker.memory_used_mb !== null ? `${worker.memory_used_mb} / ` : ""}
                      {worker.memory_mb} MB
                    </TableCell>
                    <TableCell className="hidden xl:table-cell">
                      <div className="flex flex-wrap gap-1">
                        {worker.capabilities.slice(0, 4).map((capability) => (
                          <Badge key={capability} variant="neutral">
                            {capability}
                          </Badge>
                        ))}
                        {worker.capabilities.length > 4 ? (
                          <Badge variant="outline">+{worker.capabilities.length - 4}</Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatRelative(worker.last_heartbeat_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </>
  );
}

function Stat({ label, value, icon: Icon }: { label: string; value: string; icon: typeof Users }) {
  return (
    <Card>
      <CardContent className="flex items-center justify-between p-4">
        <div>
          <p className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</p>
          <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
        </div>
        <Icon className="size-4 text-muted-foreground" aria-hidden="true" />
      </CardContent>
    </Card>
  );
}
