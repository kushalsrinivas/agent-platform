"use client";

import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogOut, Settings, UserRound } from "lucide-react";
import Link from "next/link";

import { useWorkspace } from "@/stores/workspace";

export function UserMenu() {
  const queryClient = useQueryClient();
  const apiKey = useWorkspace((state) => state.apiKey);
  const orgId = useWorkspace((state) => state.orgId);
  const openConnection = useWorkspace((state) => state.openConnection);
  const signOut = useWorkspace((state) => state.signOut);

  return (
    <details className="group relative">
      <summary
        className="grid size-9 cursor-pointer list-none place-items-center rounded-full border border-border bg-muted text-[11px] font-semibold uppercase text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden"
        aria-label="User menu"
      >
        <UserRound className="size-4" aria-hidden="true" />
      </summary>
      <div className="absolute right-0 z-50 mt-2 w-60 rounded-lg border border-border bg-card p-1.5 shadow-xl">
        <div className="border-b border-border px-2.5 py-2">
          <p className="text-xs font-medium">{orgId || "No organization"}</p>
          <p className="mt-0.5 font-mono text-[10px] text-muted-foreground">
            {apiKey ? `${apiKey.slice(0, 8)}…` : "server credentials"}
          </p>
        </div>
        <button
          type="button"
          onClick={openConnection}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-xs hover:bg-muted"
        >
          <KeyRound className="size-3.5" /> Connection settings
        </button>
        <Link
          href="/settings"
          className="flex items-center gap-2 rounded-md px-2.5 py-2 text-xs hover:bg-muted"
        >
          <Settings className="size-3.5" /> Workspace settings
        </Link>
        <button
          type="button"
          onClick={() => {
            signOut();
            queryClient.clear();
          }}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-xs text-danger hover:bg-muted"
        >
          <LogOut className="size-3.5" /> Disconnect
        </button>
      </div>
    </details>
  );
}
