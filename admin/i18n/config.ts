export const locales = ["en", "zh-CN"] as const;

export type Locale = (typeof locales)[number];

export const defaultLocale: Locale = "en";
export const localeCookieName = "aginex_locale";
export const appTimeZone = "UTC";

export type LocaleDirection = "ltr" | "rtl";

const localeDirections: Record<Locale, LocaleDirection> = {
  en: "ltr",
  "zh-CN": "ltr",
};

const languageRangePattern = /^(?:\*|[a-z]{1,8}(?:-[a-z0-9]{1,8})*)$/i;
const qualityPattern = /^(?:0(?:\.\d{0,3})?|1(?:\.0{0,3})?)$/;

export function isLocale(value: unknown): value is Locale {
  return (
    typeof value === "string" && (locales as readonly string[]).includes(value)
  );
}

export function normalizeLocale(
  value: string | null | undefined,
): Locale | null {
  const candidate = value?.trim();
  if (
    !candidate ||
    !languageRangePattern.test(candidate) ||
    candidate === "*"
  ) {
    return null;
  }

  if (isLocale(candidate)) {
    return candidate;
  }

  const subtags = candidate.toLowerCase().split("-");
  if (subtags[0] === "en") {
    return "en";
  }
  if (subtags[0] !== "zh") {
    return null;
  }

  if (subtags.length === 1) {
    return "zh-CN";
  }

  const isTraditional =
    subtags.includes("hant") ||
    subtags.some((subtag) => ["hk", "mo", "tw"].includes(subtag));
  if (isTraditional) {
    return null;
  }

  const isSimplified =
    subtags.includes("hans") ||
    subtags.some((subtag) => ["cn", "sg"].includes(subtag));
  return isSimplified ? "zh-CN" : null;
}

export function getLocaleDirection(locale: Locale): LocaleDirection {
  return localeDirections[locale];
}

export function negotiateLocale(
  acceptLanguage: string | null | undefined,
): Locale {
  const preferences = (acceptLanguage ?? "")
    .split(",")
    .map(parsePreference)
    .filter((preference) => preference !== null)
    .sort(
      (left, right) => right.quality - left.quality || left.index - right.index,
    );

  for (const preference of preferences) {
    if (preference.range === "*") {
      return defaultLocale;
    }

    const locale = normalizeLocale(preference.range);
    if (locale) {
      return locale;
    }
  }

  return defaultLocale;
}

type LanguagePreference = {
  range: string;
  quality: number;
  index: number;
};

function parsePreference(
  entry: string,
  index: number,
): LanguagePreference | null {
  const parts = entry.split(";").map((part) => part.trim());
  const range = parts[0];
  if (!range || parts.length > 2 || !languageRangePattern.test(range)) {
    return null;
  }

  let quality = 1;
  if (parts.length === 2) {
    const qualityMatch = /^q\s*=\s*(.+)$/i.exec(parts[1]);
    if (!qualityMatch || !qualityPattern.test(qualityMatch[1])) {
      return null;
    }
    quality = Number(qualityMatch[1]);
  }

  if (quality === 0) {
    return null;
  }

  return { range, quality, index };
}
