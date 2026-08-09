"use client";

import { CircleAlert, RotateCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { LocaleSwitcher } from "@/components/locale-switcher";
import "./setup.css";

export default function SetupError({ reset }: { reset: () => void }) {
  const t = useTranslations("Setup");
  return (
    <main className="setup-probe setup-probe--error" role="alert">
      <LocaleSwitcher />
      <CircleAlert aria-hidden size={26} />
      <p className="eyebrow">{t("route.error.eyebrow")}</p>
      <h1>{t("route.error.title")}</h1>
      <p>{t("route.error.description")}</p>
      <button className="button" onClick={reset} type="button">
        <RotateCw aria-hidden size={16} />
        {t("route.error.action")}
      </button>
    </main>
  );
}
