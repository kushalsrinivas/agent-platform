"use client";

import { Activity, GitBranch, LayoutDashboard, Menu } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";

import { ExecutionDrawer } from "@/components/executions/execution-drawer";
import { NewExecutionDialog } from "@/components/executions/new-execution-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/drawer";
import { ThemeToggle } from "@/components/ui/theme-toggle";
import { cn } from "@/lib/utils";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutDashboard },
  { href: "/executions", label: "Executions", icon: Activity },
  { href: "/workflows", label: "Workflows", icon: GitBranch },
];

function pageTitle(pathname: string): string {
  if (pathname === "/") return "Overview";
  if (pathname.startsWith("/executions/")) return "Execution detail";
  if (pathname.startsWith("/executions")) return "Executions";
  if (pathname.startsWith("/workflows")) return "Workflows";
  return "Agent Platform";
}

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <nav className="space-y-1" aria-label="Main">
      {NAV.map((item) => {
        const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
        const Icon = item.icon;
        return (
          <Link
            key={item.href}
            href={item.href}
            onClick={onNavigate}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors",
              active
                ? "bg-muted font-medium text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            <Icon className="size-4" aria-hidden="true" />
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-14 items-center gap-2.5 border-b border-border px-4">
        <span className="grid size-8 place-items-center rounded-md bg-primary text-sm font-semibold text-primary-foreground">
          AP
        </span>
        <span className="text-sm font-semibold tracking-tight">Agent Platform</span>
      </div>

      <div className="flex-1 px-3 py-4">
        <p className="mb-2 px-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          Workspace
        </p>
        <NavLinks onNavigate={onNavigate} />
      </div>

      <div className="space-y-2 border-t border-border px-4 py-4 text-xs text-muted-foreground">
        <div className="flex items-center justify-between">
          <span>Environment</span>
          <Badge variant="outline">local</Badge>
        </div>
        <div className="flex items-center justify-between">
          <span>API</span>
          <span className="font-mono text-[11px]">:8080</span>
        </div>
        <p className="pt-1 leading-relaxed">Demo data. Live API wiring lands in Phase 04.</p>
      </div>
    </div>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 border-r border-border bg-card/40 md:flex md:flex-col">
        <SidebarContent />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-border bg-background/85 px-4 backdrop-blur md:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="md:hidden"
            onClick={() => setMobileOpen(true)}
            aria-label="Open navigation"
          >
            <Menu className="size-4" />
          </Button>

          <p className="min-w-0 flex-1 truncate text-sm font-medium">{pageTitle(pathname)}</p>

          <Badge variant="outline" className="hidden sm:inline-flex">
            demo data
          </Badge>
          <ThemeToggle />
          <NewExecutionDialog />
        </header>

        <main className="mx-auto w-full max-w-7xl flex-1 px-4 py-6 md:px-6 md:py-8">
          {children}
        </main>
      </div>

      <Drawer
        open={mobileOpen}
        onOpenChange={setMobileOpen}
        title="Navigation"
        className="max-w-xs"
      >
        <SidebarContent onNavigate={() => setMobileOpen(false)} />
      </Drawer>

      <ExecutionDrawer />
    </div>
  );
}
