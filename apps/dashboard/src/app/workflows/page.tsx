"use client";

import { PageHeader } from "@/components/layout/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { demoWorkflows } from "@/lib/demo-data";
import { formatDateTime } from "@/lib/utils";

const STATUS_VARIANT = {
  draft: "warning",
  published: "success",
  deprecated: "neutral",
} as const;

export default function WorkflowsPage() {
  return (
    <>
      <PageHeader
        title="Workflows"
        description="Published graphs are immutable. An execution pins exact agent versions, so editing a workflow never changes a running run."
      />

      <Card>
        <CardHeader>
          <CardTitle>Workflow definitions</CardTitle>
          <CardDescription>
            Demo fixtures. Creation and publishing land with the control-plane API.
          </CardDescription>
        </CardHeader>
        <CardContent className="px-2 pb-2">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Version</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="hidden sm:table-cell">Nodes</TableHead>
                <TableHead className="hidden md:table-cell">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {demoWorkflows.map((workflow) => (
                <TableRow key={workflow.id}>
                  <TableCell>
                    <p className="text-sm font-medium">{workflow.name}</p>
                    <p className="font-mono text-[11px] text-muted-foreground">{workflow.id}</p>
                  </TableCell>
                  <TableCell className="font-mono text-xs">v{workflow.version}</TableCell>
                  <TableCell>
                    <Badge variant={STATUS_VARIANT[workflow.status]}>{workflow.status}</Badge>
                  </TableCell>
                  <TableCell className="hidden text-sm text-muted-foreground sm:table-cell">
                    {workflow.nodeCount}
                  </TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                    {formatDateTime(workflow.updatedAt)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </>
  );
}
