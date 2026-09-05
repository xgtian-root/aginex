import { createTranslator } from "next-intl";
import { describe, expect, it } from "vitest";
import en from "../messages/en.json";
import zhCN from "../messages/zh-CN.json";

const typedChineseCatalog: typeof en = zhCN;
const catalogs = [
  ["en", en],
  ["zh-CN", typedChineseCatalog],
] as const;

describe("message catalogs", () => {
  it("keeps exact recursive key parity", () => {
    expect(Object.keys(flatten(zhCN)).sort()).toEqual(
      Object.keys(flatten(en)).sort(),
    );
  });

  it.each(catalogs)(
    "keeps every %s message non-empty and ICU-valid",
    (locale, messages) => {
      const flattened = flatten(messages);
      const translate = createTranslator({ locale, messages });

      for (const [key, message] of Object.entries(flattened)) {
        expect(message.trim(), key).not.toBe("");
        expect(() =>
          translate(key as never, placeholderValues(message) as never),
        ).not.toThrow();
      }
    },
  );

  it("keeps ICU argument names aligned between locales", () => {
    const english = flatten(en);
    const chinese = flatten(zhCN);

    for (const [key, message] of Object.entries(english)) {
      expect(argumentNames(chinese[key]), key).toEqual(argumentNames(message));
    }
  });
});

function flatten(
  value: Record<string, unknown>,
  prefix = "",
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof child === "string") {
      result[path] = child;
    } else if (isRecord(child)) {
      Object.assign(result, flatten(child, path));
    } else {
      throw new TypeError(`Message ${path} must be a string or object.`);
    }
  }
  return result;
}

function placeholderValues(message: string): Record<string, string | number> {
  return Object.fromEntries(
    argumentNames(message).map((name) => [
      name,
      name === "count" || name === "status" ? 1 : "value",
    ]),
  );
}

function argumentNames(message: string): string[] {
  return [
    ...new Set(
      Array.from(
        message.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)(?:,|\})/g),
        (match) => match[1],
      ),
    ),
  ].sort();
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
