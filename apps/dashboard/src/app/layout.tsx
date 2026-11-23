import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";

import "@xyflow/react/dist/style.css";
import "./globals.css";

import { AppShell } from "@/components/layout/app-shell";
import { Providers } from "@/components/providers";

export const metadata: Metadata = {
  title: {
    default: "Agent Platform",
    template: "%s · Agent Platform",
  },
  description: "Operate versioned agent workflows with durable execution state.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      className={`${GeistSans.variable} ${GeistMono.variable}`}
    >
      <body>
        <Providers>
          <AppShell>{children}</AppShell>
        </Providers>
      </body>
    </html>
  );
}
