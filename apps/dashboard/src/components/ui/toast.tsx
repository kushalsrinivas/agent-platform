"use client";

import { useTheme } from "next-themes";
import { Toaster as SonnerToaster, toast } from "sonner";

export function Toaster() {
  const { resolvedTheme } = useTheme();
  return (
    <SonnerToaster
      theme={resolvedTheme === "light" ? "light" : "dark"}
      position="bottom-right"
      richColors
      closeButton
    />
  );
}

export { toast };
