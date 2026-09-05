import { redirect } from "next/navigation";
import { RuntimeUnavailable } from "@/components/runtime-unavailable";
import { getRuntimeMode } from "@/lib/runtime-mode.server";

export const dynamic = "force-dynamic";

export default async function Home() {
  const mode = await getRuntimeMode();
  if (mode === "setup") redirect("/setup");
  if (mode === "application") redirect("/dashboard");
  return <RuntimeUnavailable retryHref="/" />;
}
