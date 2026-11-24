"use client";

import {
  Activity,
  Bot,
  Boxes,
  ClipboardCheck,
  GitBranch,
  LayoutDashboard,
  Menu,
  Plug,
  Search,
  Settings,
  Users,
  Wrench,
} from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";

import { ExecutionDrawer } from "@/components/executions/execution-drawer";
import { NewExecutionDialog } from "@/components/executions/new-execution-dialog";
import { ConnectionDialog } from "@/components/layout/connection-dialog";
import { GlobalSearch } from "@/components/layout/global-search";
import { Notifications } from "@/components/layout/notifications";
import { UserMenu } from "@/components/layout/user-menu";
import { WorkspaceSelectors } from "@/components/layout/workspace-selectors";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/drawer";
import { ThemeToggle } from "@/components/ui/theme-toggle";
import { useOrganizations, useUnauthorized } from "@/lib/api/hooks";
import { cn } from "@/lib/utils";
import { useWorkspace } from "@/stores/workspace";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutDashboard },
  { href: "/executions", label: "Executions", icon: Activity },
  { href: "/agents", label: "Agents", icon: Bot },
  { href: "/workflows", label: "Workflows", icon: GitBranch },
  { href: "/approvals", label: "Approvals", icon: ClipboardCheck },
];

const OPS = [
  { href: "/workers", label: "Workers", icon: Users },
  { href: "/tools", label: "Tools", icon: Wrench },
  { href: "/mcp", label: "MCP", icon: Plug },
  { href: "/usage", label: "Usage", icon: Boxes },
];

function pageTitle(pathname: string): string {
  if (pathname === "/") return "Overview";
  if (pathname === "/workflows/new" || pathname.endsWith("/edit")) return "Workflow builder";
  if (pathname.startsWith("/executions/")) return "Execution explorer";
  if (pathname.startsWith("/agents/")) return "Agent";
  if (pathname.startsWith("/workflows/")) return "Workflow";
  const match = [...NAV, ...OPS, { href: "/settings", label: "Settings" }].find(
    (item) => item.href !== "/" && pathname.startsWith(item.href),
  );
  return match?.label ?? "Agent Platform";
}

function NavLink({
  item,
  pathname,
  onNavigate,
}: {
  item: { href: string; label: string; icon: typeof Activity };
  pathname: string;
  onNavigate?: () => void;
}) {
  const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
  const Icon = item.icon;
  return (
    <Link
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
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  const openConnection = useWorkspace((state) => state.openConnection);
  return (
    <div className="flex h-full flex-col">
      <Link
        href="/"
        onClick={onNavigate}
        className="flex h-14 items-center gap-2.5 border-b border-border px-4"
      >
        <span className="grid size-8 place-items-center rounded-md bg-primary text-sm font-semibold text-primary-foreground">
          AP
        </span>
        <span className="text-sm font-semibold tracking-tight">Agent Platform</span>
      </Link>

      <div className="scrollbar-thin flex-1 overflow-y-auto px-3 py-4">
        <p className="mb-2 px-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          Workspace
        </p>
        <nav className="space-y-1" aria-label="Workspace">
          {NAV.map((item) => (
            <NavLink key={item.href} item={item} pathname={pathname} onNavigate={onNavigate} />
          ))}
        </nav>
        <p className="mb-2 mt-6 px-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          Operations
        </p>
        <nav className="space-y-1" aria-label="Operations">
          {OPS.map((item) => (
            <NavLink key={item.href} item={item} pathname={pathname} onNavigate={onNavigate} />
          ))}
        </nav>
      </div>

      <div className="space-y-2 border-t border-border px-3 py-3 text-xs text-muted-foreground">
        <NavLink
          item={{ href: "/settings", label: "Settings", icon: Settings }}
          pathname={pathname}
          onNavigate={onNavigate}
        />
        <button
          type="button"
          onClick={openConnection}
          className="w-full rounded-md px-3 py-1.5 text-left text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          Connection & API key
        </button>
      </div>
    </div>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const organizations = useOrganizations();
  const openConnection = useWorkspace((state) => state.openConnection);
  const unauthorized = useUnauthorized(organizations.error);
  const builder = pathname === "/workflows/new" || pathname.endsWith("/edit");

  useEffect(() => {
    if (unauthorized) openConnection();
  }, [openConnection, unauthorized]);

  useEffect(() => {
    function shortcut(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchOpen(true);
      }
    }
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  }, []);

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 border-r border-border bg-card/40 md:flex md:flex-col">
        <SidebarContent />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-border bg-background/85 px-3 backdrop-blur md:px-5">
          <Button
            variant="ghost"
            size="icon"
            className="md:hidden"
            onClick={() => setMobileOpen(true)}
            aria-label="Open navigation"
          >
            <Menu className="size-4" />
          </Button>
          <p className="hidden min-w-0 text-sm font-medium sm:block md:hidden lg:block">
            {pageTitle(pathname)}
          </p>
          <div className="min-w-0 flex-1">
            <WorkspaceSelectors compact />
          </div>
          <Button
            variant="outline"
            size="sm"
            className="hidden gap-2 text-muted-foreground sm:flex"
            onClick={() => setSearchOpen(true)}
          >
            <Search className="size-3.5" aria-hidden="true" />
            Search
            <kbd className="rounded border border-border px-1 py-0.5 font-mono text-[10px]">⌘K</kbd>
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="sm:hidden"
            onClick={() => setSearchOpen(true)}
            aria-label="Search"
          >
            <Search className="size-4" />
          </Button>
          <Notifications />
          <ThemeToggle />
          <UserMenu />
          <NewExecutionDialog />
        </header>

        {unauthorized ? (
          <div className="border-b border-warning/30 bg-warning/10 px-4 py-2 text-xs text-warning md:px-6">
            The dashboard is not authorized. Add an API key to load live data.{" "}
            <button type="button" className="font-medium underline" onClick={openConnection}>
              Connect
            </button>
          </div>
        ) : null}

        <main
          className={cn(
            "mx-auto w-full flex-1",
            builder ? "max-w-none px-2 py-3 md:px-3" : "max-w-7xl px-4 py-6 md:px-6 md:py-8",
          )}
        >
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
      <ConnectionDialog />
      {searchOpen ? <GlobalSearch open onOpenChange={setSearchOpen} /> : null}
    </div>
  );
}
