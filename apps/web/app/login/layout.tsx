import { redirect } from "next/navigation";
import { RuntimeUnavailable } from "@/components/runtime-unavailable";
import { getRuntimeMode } from "@/lib/runtime-mode.server";

export const dynamic = "force-dynamic";

export default async function LoginLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const mode = await getRuntimeMode();
  if (mode === "setup") redirect("/setup");
  if (mode === "unknown") {
    return <RuntimeUnavailable retryHref="/login" />;
  }
  return children;
}
