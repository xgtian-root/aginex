import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.test");
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("ApiError", () => {
  it("uses the problem detail as its message", async () => {
    const { ApiError } = await import("./api");
    const error = new ApiError(
      {
        type: "about:blank",
        title: "Permission denied",
        detail: "Your role does not grant products:delete.",
        status: 403,
        instance: "/api/v1/products/1",
        code: "RESOURCE_FORBIDDEN",
        requestId: "request-1",
      },
      new Response(null, { status: 403 }),
    );

    expect(error.message).toBe("Your role does not grant products:delete.");
    expect(error.problem.status).toBe(403);
  });
});

describe("runtime API safety", () => {
  it("rejects a malformed successful mode response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>().mockResolvedValue(Response.json({ mode: "maybe" })),
    );
    const { getSystemMode } = await import("./api");

    await expect(getSystemMode()).rejects.toMatchObject({
      problem: {
        code: "REQUEST_FAILED",
        detail: "The API returned an invalid runtime state response.",
      },
    });
  });

  it("refreshes a stale cross-tab CSRF token and retries only once", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({
          token: "first-tab-token",
          headerName: "X-CSRF-Token",
        }),
      )
      .mockResolvedValueOnce(
        Response.json(
          {
            type: "about:blank",
            title: "Request denied",
            detail: "CSRF token or request origin validation failed.",
            status: 403,
            instance: "/api/v1/setup/database/test",
            code: "CSRF_FORBIDDEN",
            requestId: "request-1",
          },
          {
            status: 403,
            headers: { "Content-Type": "application/problem+json" },
          },
        ),
      )
      .mockResolvedValueOnce(
        Response.json({
          token: "second-tab-token",
          headerName: "X-CSRF-Token",
        }),
      )
      .mockResolvedValueOnce(Response.json({ status: "ok" }));
    vi.stubGlobal("fetch", fetcher);
    const { testSetupDatabase } = await import("./api");

    await expect(
      testSetupDatabase({
        database: {
          driver: "sqlite",
          sqlite: { directory: "data", filename: "aginex.db" },
        },
      }),
    ).resolves.toEqual({ status: "ok" });
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(requestHeader(fetcher.mock.calls[1])).toBe("first-tab-token");
    expect(requestHeader(fetcher.mock.calls[3])).toBe("second-tab-token");
  });
});

function requestHeader(call: Parameters<typeof fetch>) {
  const [input, init] = call;
  const headers =
    input instanceof Request ? input.headers : new Headers(init?.headers);
  return headers.get("X-CSRF-Token");
}
