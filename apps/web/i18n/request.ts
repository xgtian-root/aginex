import { cookies, headers } from "next/headers";
import { getRequestConfig } from "next-intl/server";
import {
  appTimeZone,
  isLocale,
  type Locale,
  localeCookieName,
  negotiateLocale,
} from "./config";

const messageLoaders = {
  en: () => import("../messages/en.json"),
  "zh-CN": () => import("../messages/zh-CN.json"),
} satisfies Record<Locale, () => Promise<{ default: Record<string, unknown> }>>;

export default getRequestConfig(async () => {
  const cookieStore = await cookies();
  const cookieLocale = cookieStore.get(localeCookieName)?.value;
  const locale = isLocale(cookieLocale)
    ? cookieLocale
    : negotiateLocale((await headers()).get("accept-language"));

  return {
    locale,
    messages: (await messageLoaders[locale]()).default,
    timeZone: appTimeZone,
  };
});
