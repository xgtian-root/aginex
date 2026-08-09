"use client";

import { Languages, LoaderCircle } from "lucide-react";
import { useRouter } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { useEffect, useState, useTransition } from "react";
import {
  getLocaleDirection,
  isLocale,
  type Locale,
  localeCookieName,
  locales,
} from "@/i18n/config";

const localeMaxAge = 60 * 60 * 24 * 365;

export function LocaleSwitcher() {
  const locale = useLocale();
  const router = useRouter();
  const t = useTranslations("LocaleSwitcher");
  const [selectedLocale, setSelectedLocale] = useState<Locale>(locale);
  const [isPending, startTransition] = useTransition();

  useEffect(() => setSelectedLocale(locale), [locale]);

  function changeLocale(value: string) {
    if (!isLocale(value) || value === locale) return;

    setSelectedLocale(value);
    // biome-ignore lint/suspicious/noDocumentCookie: This allowlisted preference must be readable by the Next.js request configuration.
    document.cookie = serializeLocaleCookie(value, window.location.protocol);
    document.documentElement.lang = value;
    document.documentElement.dir = getLocaleDirection(value);
    startTransition(() => router.refresh());
  }

  return (
    <label className="locale-switcher">
      {isPending ? (
        <LoaderCircle className="spin" aria-hidden size={16} />
      ) : (
        <Languages aria-hidden size={16} />
      )}
      <span>{t("label")}</span>
      <select
        aria-busy={isPending}
        aria-label={t("ariaLabel")}
        disabled={isPending}
        onChange={(event) => changeLocale(event.target.value)}
        value={selectedLocale}
      >
        {locales.map((option) => (
          <option key={option} value={option}>
            {option === "en" ? t("english") : t("simplifiedChinese")}
          </option>
        ))}
      </select>
      {isPending && <span className="sr-only">{t("changing")}</span>}
    </label>
  );
}

export function serializeLocaleCookie(
  locale: Locale,
  protocol: string,
): string {
  const secure = protocol === "https:" ? "; Secure" : "";
  return `${localeCookieName}=${encodeURIComponent(locale)}; Path=/; Max-Age=${localeMaxAge}; SameSite=Lax${secure}`;
}
