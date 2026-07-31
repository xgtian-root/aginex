import { describe, expect, it } from "vitest";
import { ApiError } from "./api";

describe("ApiError", () => {
  it("uses the problem detail as its message", () => {
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
