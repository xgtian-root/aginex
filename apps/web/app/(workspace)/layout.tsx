import { AppShell } from "@/components/app-shell";
import "@/components/app-shell.css";

export default function WorkspaceLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return <AppShell>{children}</AppShell>;
}
