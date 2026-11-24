"use client";

import { useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { useWorkspace } from "@/stores/workspace";

export function ConnectionDialog() {
  const queryClient = useQueryClient();
  const open = useWorkspace((state) => state.connectionOpen);
  const apiKey = useWorkspace((state) => state.apiKey);
  const close = useWorkspace((state) => state.closeConnection);
  const setApiKey = useWorkspace((state) => state.setApiKey);
  const [value, setValue] = useState(apiKey);

  function connect(key: string) {
    setApiKey(key.trim());
    void queryClient.invalidateQueries();
    close();
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => (next ? null : close())}
      title="Connect to the platform"
      description="Use an API key for this browser session. It is sent only to the dashboard proxy, which exchanges it for the platform API."
      footer={
        <>
          <Button variant="ghost" onClick={() => connect("")}>
            Use server credentials
          </Button>
          <Button onClick={() => connect(value)}>Connect</Button>
        </>
      }
    >
      <div className="space-y-4">
        <label htmlFor="dashboard-api-key" className="block text-sm font-medium">
          API key
        </label>
        <div className="relative">
          <KeyRound className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground" />
          <input
            id="dashboard-api-key"
            type="password"
            autoComplete="off"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="apk_…"
            className="h-9 w-full rounded-md border border-input bg-background pl-9 pr-3 font-mono text-xs"
          />
        </div>
        <p className="text-xs leading-relaxed text-muted-foreground">
          Create a key in <span className="font-mono">Settings → API keys</span> or with the CLI. A
          project-scoped key only sees its project. If the deployment injects{" "}
          <span className="font-mono">DASHBOARD_API_KEY</span>, you can use server credentials.
        </p>
      </div>
    </Modal>
  );
}
