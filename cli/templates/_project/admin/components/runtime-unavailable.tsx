import { RotateCw, ShieldAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import { LocaleSwitcher } from "@/components/locale-switcher";
import "./runtime-unavailable.css";

export function RuntimeUnavailable({
  retryHref,
  title,
}: {
  retryHref: string;
  title?: string;
}) {
  const t = useTranslations("RuntimeUnavailable");
  return (
    <main className="runtime-unavailable">
      <div className="runtime-unavailable__index" aria-hidden>
        {t("statusCode")}
      </div>
      <section aria-labelledby="runtime-unavailable-title">
        <LocaleSwitcher />
        <div className="runtime-unavailable__mark">
          <ShieldAlert aria-hidden size={21} strokeWidth={1.7} />
          {t("mark")}
        </div>
        <p className="eyebrow">{t("eyebrow")}</p>
        <h1 id="runtime-unavailable-title">{title ?? t("defaultTitle")}</h1>
        <p className="runtime-unavailable__copy">{t("description")}</p>
        <a className="button" href={retryHref}>
          <RotateCw aria-hidden size={16} />
          {t("action")}
        </a>
      </section>
      <p className="runtime-unavailable__footnote">{t("footnote")}</p>
    </main>
  );
}
