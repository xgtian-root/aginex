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

describe("management catalogs", () => {
  it("loads every catalog page when the API caps page size", async () => {
    const firstRole = roleResponse("role-1", "Support");
    const secondRole = roleResponse("role-2", "Operations");
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({
          items: [firstRole],
          page: 1,
          pageSize: 1,
          total: 2,
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          items: [secondRole],
          page: 2,
          pageSize: 1,
          total: 2,
        }),
      );
    vi.stubGlobal("fetch", fetcher);
    const { listRoleCatalog } = await import("./api");

    await expect(listRoleCatalog()).resolves.toEqual([firstRole, secondRole]);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(requestURL(fetcher.mock.calls[1]).searchParams.get("page")).toBe(
      "2",
    );
  });
});

describe("file upload policy", () => {
  it("updates pending values with revision, CSRF, and idempotency headers", async () => {
    const settings = {
      runtimeActiveProfileId: "profile-1",
      pendingActiveProfileId: "profile-1",
      environmentManaged: false,
      restartRequired: true,
      revision: 4,
      runtimeRevision: 3,
      unboundFileCount: 0,
      runtimeFileUploadPolicy: {
        maxUploadBytes: 10 * 1024 * 1024,
        resumableUploadsEnabled: false,
      },
      pendingFileUploadPolicy: {
        maxUploadBytes: 64 * 1024 * 1024,
        resumableUploadsEnabled: true,
      },
    };
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({
          token: "csrf-token",
          headerName: "X-CSRF-Token",
        }),
      )
      .mockResolvedValueOnce(
        Response.json(settings, { headers: { ETag: '"revision-4"' } }),
      );
    vi.stubGlobal("fetch", fetcher);
    const { updateFileUploadPolicy } = await import("./api");

    await expect(
      updateFileUploadPolicy(settings.pendingFileUploadPolicy, '"revision-3"'),
    ).resolves.toEqual({ value: settings, etag: '"revision-4"' });

    const updateCall = fetcher.mock.calls[1];
    expect(requestURL(updateCall).pathname).toBe(
      "/api/v1/storage-settings/file-upload-policy",
    );
    expect(updateCall[1]?.method).toBe("PUT");
    const headers = requestHeaders(updateCall);
    expect(headers.get("If-Match")).toBe('"revision-3"');
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
    expect(headers.get("Idempotency-Key")).toBeTruthy();
  });
});

describe("resumable file transfer API", () => {
  it("lists incomplete sessions and signs a bounded part batch", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({ items: [], page: 1, pageSize: 20, total: 0 }),
      )
      .mockResolvedValueOnce(
        Response.json({
          token: "csrf-token",
          headerName: "X-CSRF-Token",
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          items: [
            {
              partNumber: 2,
              size: 33_554_432,
              upload: { method: "PUT", url: "/part/2", headers: {} },
            },
          ],
        }),
      );
    vi.stubGlobal("fetch", fetcher);
    const { listIncompleteUploadSessions, signUploadParts } = await import(
      "./api"
    );

    await listIncompleteUploadSessions();
    await expect(signUploadParts("session-1", [2])).resolves.toEqual([
      expect.objectContaining({ partNumber: 2 }),
    ]);

    expect(requestURL(fetcher.mock.calls[0]).searchParams.get("state")).toBe(
      "incomplete",
    );
    const signCall = fetcher.mock.calls[2];
    expect(requestURL(signCall).pathname).toBe(
      "/api/v1/files/upload-sessions/session-1/parts/sign",
    );
    expect(requestHeaders(signCall).get("X-CSRF-Token")).toBe("csrf-token");
    expect(JSON.parse(String(signCall[1]?.body))).toEqual({ partNumbers: [2] });
  });

  it("requests a server-controlled preview or download URL", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        Response.json({ method: "GET", url: "/content", headers: {} }),
      );
    vi.stubGlobal("fetch", fetcher);
    const { getFileURL } = await import("./api");

    await getFileURL("file-1", "download");

    const url = requestURL(fetcher.mock.calls[0]);
    expect(url.pathname).toBe("/api/v1/files/file-1/url");
    expect(url.searchParams.get("purpose")).toBe("download");
    expect(url.searchParams.has("disposition")).toBe(false);
  });

  it("uploads a local part with CSRF, credentials, progress, and its ETag", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json({
        token: "csrf-token",
        headerName: "X-CSRF-Token",
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    const request = new FakeXMLHttpRequest();
    vi.stubGlobal(
      "XMLHttpRequest",
      vi.fn(function XMLHttpRequest() {
        return request;
      }),
    );
    const { uploadSignedBlob } = await import("./api");
    const progress = vi.fn();

    await expect(
      uploadSignedBlob(
        {
          method: "PUT",
          url: "/api/v1/files/upload-sessions/session-1/parts/2?signature=x",
          headers: {
            "Content-Type": "application/octet-stream",
            "Content-Length": "4",
          },
          expiresAt: "2026-08-11T01:00:00Z",
        },
        new Blob(["part"]),
        progress,
      ),
    ).resolves.toBe('"part-etag"');

    expect(request.withCredentials).toBe(true);
    expect(request.headers.get("x-csrf-token")).toBe("csrf-token");
    expect(request.headers.get("content-type")).toBe(
      "application/octet-stream",
    );
    expect(progress).toHaveBeenCalledWith(50);
    expect(progress).toHaveBeenLastCalledWith(100);
  });

  it("aborts only the signed upload attached to its AbortSignal", async () => {
    const request = new FakeXMLHttpRequest();
    request.deferLoad = true;
    vi.stubGlobal(
      "XMLHttpRequest",
      vi.fn(function XMLHttpRequest() {
        return request;
      }),
    );
    const { uploadSignedBlob } = await import("./api");
    const controller = new AbortController();
    const uploading = uploadSignedBlob(
      {
        method: "PUT",
        url: "https://objects.test/upload/part-1",
        headers: { "Content-Type": "application/octet-stream" },
        expiresAt: "2026-08-11T01:00:00Z",
      },
      new Blob(["part"]),
      vi.fn(),
      controller.signal,
    );

    controller.abort();

    await expect(uploading).rejects.toMatchObject({ name: "AbortError" });
    expect(request.aborted).toBe(true);
  });
});

function requestHeader(call: Parameters<typeof fetch>) {
  return requestHeaders(call).get("X-CSRF-Token");
}

function requestHeaders(call: Parameters<typeof fetch>) {
  const [input, init] = call;
  return input instanceof Request ? input.headers : new Headers(init?.headers);
}

function requestURL(call: Parameters<typeof fetch>) {
  const [input] = call;
  return new URL(input instanceof Request ? input.url : input.toString());
}

function roleResponse(id: string, name: string) {
  return {
    id,
    name,
    description: `${name} role`,
    systemManaged: false,
    userCount: 0,
    grants: [],
    createdAt: "2026-08-10T00:00:00Z",
    updatedAt: "2026-08-10T00:00:00Z",
  };
}

class FakeXMLHttpRequest {
  aborted = false;
  deferLoad = false;
  headers = new Map<string, string>();
  status = 204;
  withCredentials = false;
  private listeners = new Map<string, () => void>();
  private progressListener?: (event: {
    lengthComputable: boolean;
    loaded: number;
    total: number;
  }) => void;
  upload = {
    addEventListener: (
      _name: string,
      listener: (event: {
        lengthComputable: boolean;
        loaded: number;
        total: number;
      }) => void,
    ) => {
      this.progressListener = listener;
    },
  };

  open() {}

  setRequestHeader(name: string, value: string) {
    this.headers.set(name.toLowerCase(), value);
  }

  addEventListener(name: string, listener: () => void) {
    this.listeners.set(name, listener);
  }

  abort() {
    this.aborted = true;
    this.listeners.get("abort")?.();
  }

  getResponseHeader(name: string) {
    return name.toLowerCase() === "etag" ? '"part-etag"' : null;
  }

  send() {
    this.progressListener?.({ lengthComputable: true, loaded: 2, total: 4 });
    if (!this.deferLoad) this.listeners.get("load")?.();
  }
}
