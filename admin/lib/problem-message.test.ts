import { describe, expect, it, vi } from "vitest";
import messages from "../messages/en.json";
import { ApiError } from "./api";
import {
  localizeApiError,
  type ProblemMessageKey,
  problemMessageKeyByCode,
} from "./problem-message";

const stableMappings = [
  ["REQUEST_INVALID", "Problem.codes.REQUEST_INVALID"],
  ["AUTHENTICATION_REQUIRED", "Problem.codes.AUTHENTICATION_REQUIRED"],
  ["RESOURCE_FORBIDDEN", "Problem.codes.RESOURCE_FORBIDDEN"],
  ["RESOURCE_NOT_FOUND", "Problem.codes.RESOURCE_NOT_FOUND"],
  ["REQUEST_CONFLICT", "Problem.codes.REQUEST_CONFLICT"],
  ["REQUEST_TOO_LARGE", "Problem.codes.REQUEST_TOO_LARGE"],
  ["UNSUPPORTED_MEDIA_TYPE", "Problem.codes.UNSUPPORTED_MEDIA_TYPE"],
  ["RATE_LIMITED", "Problem.codes.RATE_LIMITED"],
  ["HEADERS_TOO_LARGE", "Problem.codes.HEADERS_TOO_LARGE"],
  ["INTERNAL_ERROR", "Problem.codes.INTERNAL_ERROR"],
  ["REQUEST_FAILED", "Problem.codes.REQUEST_FAILED"],
  ["CSRF_FORBIDDEN", "Problem.codes.CSRF_FORBIDDEN"],
  ["SETUP_DATABASE_UNAVAILABLE", "Problem.codes.SETUP_DATABASE_UNAVAILABLE"],
  [
    "SETUP_APPLICATION_INITIALIZATION_FAILED",
    "Problem.codes.SETUP_APPLICATION_INITIALIZATION_FAILED",
  ],
  [
    "SETUP_CONFIGURATION_COMMIT_FAILED",
    "Problem.codes.SETUP_CONFIGURATION_COMMIT_FAILED",
  ],
  ["SETUP_INITIALIZATION_FAILED", "Problem.codes.SETUP_INITIALIZATION_FAILED"],
  ["SETUP_IN_PROGRESS", "Problem.codes.SETUP_IN_PROGRESS"],
] as const;

describe("problemMessageKeyByCode", () => {
  it("covers every published problem message code", () => {
    expect(Object.keys(problemMessageKeyByCode).sort()).toEqual(
      Object.keys(messages.Problem.codes).sort(),
    );
  });

  it.each(stableMappings)("maps %s to %s", (code, key) => {
    expect(problemMessageKeyByCode[code]).toBe(key);
  });
});

describe("localizeApiError", () => {
  it("translates a known stable problem code", () => {
    const translate = (key: ProblemMessageKey) => `translated:${key}`;

    expect(
      localizeApiError(
        apiError("RESOURCE_FORBIDDEN", "Server permission detail."),
        translate,
        "Localized fallback",
      ),
    ).toBe("translated:Problem.codes.RESOURCE_FORBIDDEN");
  });

  it("prefers a context-specific override for a known code", () => {
    const translate = vi.fn((key: ProblemMessageKey) => `translated:${key}`);

    expect(
      localizeApiError(
        apiError(
          "AUTHENTICATION_REQUIRED",
          "The email or password is incorrect.",
          401,
        ),
        translate,
        "Localized fallback",
        { AUTHENTICATION_REQUIRED: "Localized sign-in failure" },
      ),
    ).toBe("Localized sign-in failure");
    expect(translate).not.toHaveBeenCalled();
  });

  it("returns the fallback for an unknown problem code", () => {
    expect(
      localizeApiError(
        apiError("NEW_SERVER_CODE", "Untranslated server detail."),
        identityTranslation,
        "Localized fallback",
      ),
    ).toBe("Localized fallback");
  });

  it("returns the fallback for a non-API error", () => {
    expect(
      localizeApiError(
        new TypeError("network unavailable"),
        identityTranslation,
        "Localized fallback",
      ),
    ).toBe("Localized fallback");
  });

  it("never uses the raw problem detail as the default display", () => {
    const detail = "Sensitive request detail must not be displayed.";
    const displayed = localizeApiError(
      apiError("UNKNOWN_FAILURE", detail),
      identityTranslation,
      "Localized fallback",
    );

    expect(displayed).toBe("Localized fallback");
    expect(displayed).not.toContain(detail);
  });
});

function identityTranslation(key: ProblemMessageKey) {
  return key;
}

function apiError(code: string, detail: string, status = 400) {
  return new ApiError(
    {
      type: "about:blank",
      title: "Server title",
      status,
      detail,
      instance: "/api/v1/example",
      code,
      requestId: "request-1",
    },
    new Response(null, { status }),
  );
}
