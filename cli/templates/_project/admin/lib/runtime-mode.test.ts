import { describe, expect, it, vi } from "vitest";
import {
  isSetupStatusResponse,
  isSystemModeResponse,
  probeRuntimeMode,
  resolveServerAPIURL,
} from "./runtime-mode";

describe("probeRuntimeMode", () => {
  it.each(["setup", "application"] as const)(
    "accepts the explicit %s mode",
    async (mode) => {
      const fetcher = vi
        .fn<typeof fetch>()
        .mockResolvedValue(
          Response.json({ mode }, { headers: { "Cache-Control": "no-store" } }),
        );

      await expect(
        probeRuntimeMode("http://api.internal:8080/api/v1", fetcher),
      ).resolves.toBe(mode);
      expect(fetcher).toHaveBeenCalledWith(
        new URL("http://api.internal:8080/api/v1/system/mode"),
        expect.objectContaining({ cache: "no-store" }),
      );
    },
  );

  it.each([
    new Response(null, { status: 503 }),
    Response.json({ mode: "setup-ish" }),
    Response.json({}),
  ])("fails closed for a non-authoritative response", async (response) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response);
    await expect(
      probeRuntimeMode("http://api.internal:8080", fetcher),
    ).resolves.toBe("unknown");
  });

  it("fails closed when the backend cannot be reached", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new TypeError("network unavailable"));
    await expect(
      probeRuntimeMode("http://api.internal:8080", fetcher),
    ).resolves.toBe("unknown");
  });
});

describe("runtime response validation", () => {
  it("accepts only the finite mode contract", () => {
    expect(isSystemModeResponse({ mode: "setup" })).toBe(true);
    expect(isSystemModeResponse({ mode: "application" })).toBe(true);
    expect(isSystemModeResponse({ mode: "setup-ish" })).toBe(false);
    expect(isSystemModeResponse({ mode: "setup", extra: true })).toBe(false);
    expect(isSystemModeResponse({})).toBe(false);
  });

  it("accepts only complete finite setup status values", () => {
    expect(
      isSetupStatusResponse({
        status: "initializing",
        stage: "migrating",
      }),
    ).toBe(true);
    expect(
      isSetupStatusResponse({ status: "required", stage: "surprise" }),
    ).toBe(false);
    expect(
      isSetupStatusResponse({
        status: "required",
        stage: "waiting",
        extra: true,
      }),
    ).toBe(false);
    expect(isSetupStatusResponse({ status: "required" })).toBe(false);
    expect(
      isSetupStatusResponse({
        status: "failed",
        stage: "waiting",
        code: 42,
      }),
    ).toBe(false);
  });
});

describe("resolveServerAPIURL", () => {
  it("prefers the private runtime address", () => {
    expect(
      resolveServerAPIURL({
        AGINEX_API_INTERNAL_URL: "http://api:8080",
        NEXT_PUBLIC_API_URL: "https://aginex.example",
      }),
    ).toBe("http://api:8080");
  });

  it("keeps an absolute public override as a fallback", () => {
    expect(
      resolveServerAPIURL({ NEXT_PUBLIC_API_URL: "https://aginex.example" }),
    ).toBe("https://aginex.example");
  });

  it("does not try to fetch a relative browser URL on the server", () => {
    expect(resolveServerAPIURL({ NEXT_PUBLIC_API_URL: "/" })).toBe(
      "http://127.0.0.1:8080",
    );
  });
});
