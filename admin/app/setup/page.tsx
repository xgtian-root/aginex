import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { getTranslations } from "next-intl/server";
import { RuntimeUnavailable } from "@/components/runtime-unavailable";
import { SetupWizard } from "@/components/setup-wizard";
import { getRuntimeMode } from "@/lib/runtime-mode.server";
import "./commissioning.css";

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("Metadata.setup");
  return {
    title: t("title"),
    description: t("description"),
  };
}

export const dynamic = "force-dynamic";

export default async function SetupPage() {
  const mode = await getRuntimeMode();
  if (mode === "application") redirect("/dashboard");
  if (mode === "unknown") {
    return <RuntimeUnavailable retryHref="/setup" />;
  }
  return <SetupWizard />;
}
