import { describe, expect, it } from "vitest";
import { safeLocalRedirect } from "./navigation";

describe("safeLocalRedirect", () => {
  it.each(["/dashboard", "/products?page=2", "/audit#latest"])(
    "keeps the local path %s",
    (value) => {
      expect(safeLocalRedirect(value)).toBe(value);
    },
  );

  it.each([
    "https://evil.example",
    "javascript:alert(1)",
    "//evil.example",
    "/\\evil.example",
    "/%5cevil.example",
    "/%2fevil.example",
    "/%252fevil.example",
    "/safe\nhttps://evil.example",
  ])("rejects the external or ambiguous path %s", (value) => {
    expect(safeLocalRedirect(value)).toBe("/dashboard");
  });
});
