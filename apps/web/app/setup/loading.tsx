"use client";

import { useTranslations } from "next-intl";
import { LocaleSwitcher } from "@/components/locale-switcher";
import "./setup.css";

export default function SetupRouteLoading() {
  const t = useTranslations("Setup");
  return (
    <main className="setup-route-loading" aria-live="polite" aria-busy="true">
      <LocaleSwitcher />
      <span className="setup-route-loading__rule" aria-hidden />
      <p className="eyebrow">{t("route.loading.eyebrow")}</p>
      <h1>{t("route.loading.title")}</h1>
      <p>{t("route.loading.description")}</p>
    </main>
  );
}
