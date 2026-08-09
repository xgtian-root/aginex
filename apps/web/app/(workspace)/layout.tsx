import { redirect } from "next/navigation";
import { AppShell } from "@/components/app-shell";
import { RuntimeUnavailable } from "@/components/runtime-unavailable";
import { getRuntimeMode } from "@/lib/runtime-mode.server";
import "@/components/app-shell.css";

export const dynamic = "force-dynamic";

export default async function WorkspaceLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const mode = await getRuntimeMode();
  if (mode === "setup") redirect("/setup");
  if (mode === "unknown") {
    return <RuntimeUnavailable retryHref="/" />;
  }
  return <AppShell>{children}</AppShell>;
}
