import { describe, expect, it } from "vitest";
import {
  appTimeZone,
  defaultLocale,
  getLocaleDirection,
  isLocale,
  localeCookieName,
  locales,
  negotiateLocale,
  normalizeLocale,
} from "./config";

describe("locale contract", () => {
  it("publishes stable defaults", () => {
    expect(locales).toEqual(["en", "zh-CN"]);
    expect(defaultLocale).toBe("en");
    expect(localeCookieName).toBe("aginex_locale");
    expect(appTimeZone).toBe("UTC");
  });

  it("exactly allowlists locale values suitable for a cookie", () => {
    expect(isLocale("en")).toBe(true);
    expect(isLocale("zh-CN")).toBe(true);

    for (const value of [
      "EN",
      "zh-cn",
      "zh",
      " en ",
      "fr",
      "",
      null,
      undefined,
      1,
    ]) {
      expect(isLocale(value)).toBe(false);
    }
  });
});

describe("normalizeLocale", () => {
  it.each(["en", "en-US", "en-GB", "EN-us", "en-US-u-ca-gregory"])(
    "normalizes the English variant %s",
    (value) => {
      expect(normalizeLocale(value)).toBe("en");
    },
  );

  it.each(["zh", "zh-CN", "zh-cn", "zh-Hans", "zh-Hans-CN", "zh-SG"])(
    "normalizes the Simplified Chinese variant %s",
    (value) => {
      expect(normalizeLocale(value)).toBe("zh-CN");
    },
  );

  it.each([null, undefined, "", "*", "fr-FR", "zh-Hant", "zh-TW", "en_US"])(
    "does not invent support for %s",
    (value) => {
      expect(normalizeLocale(value)).toBeNull();
    },
  );
});

describe("negotiateLocale", () => {
  it("honors quality weights before header order", () => {
    expect(negotiateLocale("en-US;q=0.4, zh-Hans-CN;q=0.9")).toBe("zh-CN");
  });

  it("uses header order to break equal-quality ties", () => {
    expect(negotiateLocale("zh;q=0.7, en;q=0.7")).toBe("zh-CN");
    expect(negotiateLocale("en;q=0.7, zh;q=0.7")).toBe("en");
  });

  it("treats a weighted wildcard as the deterministic default", () => {
    expect(negotiateLocale("fr;q=0.9, *;q=0.8, zh-CN;q=0.7")).toBe("en");
    expect(negotiateLocale("fr;q=0.9, zh;q=0.8, *;q=0.1")).toBe("zh-CN");
  });

  it("ignores invalid and zero-quality entries", () => {
    expect(
      negotiateLocale(
        "not a language, zh-CN;q=0, en-US;q=bogus, fr;q=1.001, zh-Hans;q=0.6",
      ),
    ).toBe("zh-CN");
  });

  it.each([null, undefined, "", "fr-FR", "zh;q=0, en;q=0", "en;q=-1"])(
    "falls back to English for %s",
    (value) => {
      expect(negotiateLocale(value)).toBe("en");
    },
  );
});

describe("getLocaleDirection", () => {
  it.each(locales)("marks %s as left-to-right", (locale) => {
    expect(getLocaleDirection(locale)).toBe("ltr");
  });
});
