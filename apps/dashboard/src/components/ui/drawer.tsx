"use client";

import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import type * as React from "react";

import { cn } from "@/lib/utils";

type DrawerProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
  className?: string;
};

/** Right-hand panel for drill-down without leaving the list. Built on the same dialog primitive as the modal. */
export function Drawer({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
}: DrawerProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay fixed inset-0 z-40 bg-black/50 backdrop-blur-[2px]" />
        <Dialog.Content
          className={cn(
            "drawer-content fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col border-l border-border bg-card shadow-2xl",
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
            <div className="space-y-1">
              <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
              {description ? (
                <Dialog.Description className="text-sm text-muted-foreground">
                  {description}
                </Dialog.Description>
              ) : null}
            </div>
            <Dialog.Close
              className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label="Close panel"
            >
              <X className="size-4" aria-hidden="true" />
            </Dialog.Close>
          </div>
          <div className="scrollbar-thin flex-1 overflow-y-auto px-5 py-4">{children}</div>
          {footer ? (
            <div className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>
          ) : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
