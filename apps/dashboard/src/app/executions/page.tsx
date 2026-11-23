"use client";

import { ExecutionsTable } from "@/components/executions/executions-table";
import { NewExecutionDialog } from "@/components/executions/new-execution-dialog";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useExecutionDrawer } from "@/stores/ui-store";

export default function ExecutionsPage() {
  const selectedId = useExecutionDrawer((state) => state.selectedId);

  return (
    <>
      <PageHeader
        title="Executions"
        description="Every run of a published workflow version. Dispatch, leases and retries are owned by the orchestrator; this view is read-only."
        actions={<NewExecutionDialog />}
      />

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>All executions</CardTitle>
            <CardDescription>
              Demo fixtures. The list endpoint and SSE stream arrive with the API in Phase 05.
            </CardDescription>
          </div>
          {selectedId ? (
            <span className="font-mono text-[11px] text-muted-foreground">{selectedId}</span>
          ) : null}
        </CardHeader>
        <CardContent className="px-2 pb-2">
          <ExecutionsTable />
        </CardContent>
      </Card>
    </>
  );
}
