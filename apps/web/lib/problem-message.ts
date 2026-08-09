import type messages from "../messages/en.json";
import { ApiError } from "./api";

export type KnownProblemCode = keyof typeof messages.Problem.codes;
export type ProblemMessageKey = `Problem.codes.${KnownProblemCode}`;

export const problemMessageKeyByCode = Object.freeze({
  REQUEST_INVALID: "Problem.codes.REQUEST_INVALID",
  AUTHENTICATION_REQUIRED: "Problem.codes.AUTHENTICATION_REQUIRED",
  RESOURCE_FORBIDDEN: "Problem.codes.RESOURCE_FORBIDDEN",
  RESOURCE_NOT_FOUND: "Problem.codes.RESOURCE_NOT_FOUND",
  REQUEST_CONFLICT: "Problem.codes.REQUEST_CONFLICT",
  REQUEST_TOO_LARGE: "Problem.codes.REQUEST_TOO_LARGE",
  UNSUPPORTED_MEDIA_TYPE: "Problem.codes.UNSUPPORTED_MEDIA_TYPE",
  RATE_LIMITED: "Problem.codes.RATE_LIMITED",
  HEADERS_TOO_LARGE: "Problem.codes.HEADERS_TOO_LARGE",
  INTERNAL_ERROR: "Problem.codes.INTERNAL_ERROR",
  REQUEST_FAILED: "Problem.codes.REQUEST_FAILED",
  CORS_FORBIDDEN: "Problem.codes.CORS_FORBIDDEN",
  CORS_PREFLIGHT_FORBIDDEN: "Problem.codes.CORS_PREFLIGHT_FORBIDDEN",
  CSRF_FORBIDDEN: "Problem.codes.CSRF_FORBIDDEN",
  CSRF_TOKEN_UNAVAILABLE: "Problem.codes.CSRF_TOKEN_UNAVAILABLE",
  SERVICE_UNAVAILABLE: "Problem.codes.SERVICE_UNAVAILABLE",
  SETUP_DATABASE_UNAVAILABLE: "Problem.codes.SETUP_DATABASE_UNAVAILABLE",
  SETUP_IN_PROGRESS: "Problem.codes.SETUP_IN_PROGRESS",
  SETUP_APPLICATION_INITIALIZATION_FAILED:
    "Problem.codes.SETUP_APPLICATION_INITIALIZATION_FAILED",
  SETUP_CONFIGURATION_COMMIT_FAILED:
    "Problem.codes.SETUP_CONFIGURATION_COMMIT_FAILED",
  SETUP_INITIALIZATION_FAILED: "Problem.codes.SETUP_INITIALIZATION_FAILED",
  FILE_CLEANUP_UNAVAILABLE: "Problem.codes.FILE_CLEANUP_UNAVAILABLE",
  FILE_TOO_LARGE: "Problem.codes.FILE_TOO_LARGE",
  FILE_CONTENT_INVALID: "Problem.codes.FILE_CONTENT_INVALID",
  UPLOAD_INTENT_NOT_PENDING: "Problem.codes.UPLOAD_INTENT_NOT_PENDING",
  IDEMPOTENCY_KEY_INVALID: "Problem.codes.IDEMPOTENCY_KEY_INVALID",
  IDEMPOTENCY_UNAVAILABLE: "Problem.codes.IDEMPOTENCY_UNAVAILABLE",
  IDEMPOTENCY_ACTOR_UNAVAILABLE: "Problem.codes.IDEMPOTENCY_ACTOR_UNAVAILABLE",
  IDEMPOTENCY_IN_PROGRESS: "Problem.codes.IDEMPOTENCY_IN_PROGRESS",
  IDEMPOTENCY_RESPONSE_TOO_LARGE:
    "Problem.codes.IDEMPOTENCY_RESPONSE_TOO_LARGE",
  IDEMPOTENCY_COMPLETION_FAILED: "Problem.codes.IDEMPOTENCY_COMPLETION_FAILED",
  IDEMPOTENCY_KEY_REUSED: "Problem.codes.IDEMPOTENCY_KEY_REUSED",
  IDEMPOTENCY_RESOURCE_MISSING: "Problem.codes.IDEMPOTENCY_RESOURCE_MISSING",
  IDEMPOTENCY_REPLAY_FAILED: "Problem.codes.IDEMPOTENCY_REPLAY_FAILED",
  JOBS_UNAVAILABLE: "Problem.codes.JOBS_UNAVAILABLE",
  RATE_LIMIT_ACTOR_UNAVAILABLE: "Problem.codes.RATE_LIMIT_ACTOR_UNAVAILABLE",
  RATE_LIMIT_POLICY_INVALID: "Problem.codes.RATE_LIMIT_POLICY_INVALID",
  RATE_LIMIT_UNAVAILABLE: "Problem.codes.RATE_LIMIT_UNAVAILABLE",
} as const satisfies Record<KnownProblemCode, ProblemMessageKey>);
export type ProblemMessageOverrides = Readonly<
  Partial<Record<KnownProblemCode, string>>
>;

export function localizeApiError(
  error: unknown,
  translate: (key: ProblemMessageKey) => string,
  fallback: string,
  overrides: ProblemMessageOverrides = {},
): string {
  if (!(error instanceof ApiError)) return fallback;

  const code: unknown = error.problem.code;
  if (typeof code !== "string") return fallback;

  const knownCode = code as KnownProblemCode;
  const override = overrides[knownCode];
  if (override !== undefined) return override;

  const key = problemMessageKeyByCode[knownCode];
  return key === undefined ? fallback : translate(key);
}
