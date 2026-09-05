import type { components } from "./api.generated";

export type SystemModeResponse = components["schemas"]["SystemModeResponse"];
export type SetupStatusResponse = components["schemas"]["SetupStatusResponse"];
export type RuntimeMode = SystemModeResponse["mode"] | "unknown";

const modePath = "/api/v1/system/mode";

export async function probeRuntimeMode(
  baseURL: string,
  fetcher: typeof fetch = fetch,
): Promise<RuntimeMode> {
  let endpoint: URL;
  try {
    endpoint = new URL(modePath, normalizeBaseURL(baseURL));
  } catch {
    return "unknown";
  }

  try {
    const response = await fetcher(endpoint, {
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(5_000),
    });
    if (!response.ok) return "unknown";

    const payload: unknown = await response.json();
    if (!isSystemModeResponse(payload)) return "unknown";
    return payload.mode;
  } catch {
    return "unknown";
  }
}

export function resolveServerAPIURL(
  environment: Readonly<Record<string, string | undefined>>,
): string {
  const internal = environment.AGINEX_API_INTERNAL_URL?.trim();
  if (internal) return internal;

  const publicURL = environment.NEXT_PUBLIC_API_URL?.trim();
  if (publicURL && /^https?:\/\//i.test(publicURL)) return publicURL;

  return "http://127.0.0.1:8080";
}

function normalizeBaseURL(value: string) {
  const withoutAPIPath = value.replace(/\/api\/v1\/?$/, "");
  return `${withoutAPIPath.replace(/\/$/, "")}/`;
}

export function isSystemModeResponse(
  value: unknown,
): value is SystemModeResponse {
  if (
    typeof value !== "object" ||
    value === null ||
    !("mode" in value) ||
    !hasOnlyKeys(value, ["mode"])
  ) {
    return false;
  }
  return value.mode === "setup" || value.mode === "application";
}

export function isSetupStatusResponse(
  value: unknown,
): value is SetupStatusResponse {
  if (typeof value !== "object" || value === null) return false;
  if (!("status" in value) || !("stage" in value)) return false;
  if (!hasOnlyKeys(value, ["status", "stage", "code"])) return false;
  const status = value.status;
  const stage = value.stage;
  const code = "code" in value ? value.code : undefined;
  return (
    (status === "required" ||
      status === "initializing" ||
      status === "failed") &&
    (stage === "waiting" ||
      stage === "validating_database" ||
      stage === "migrating" ||
      stage === "bootstrapping" ||
      stage === "starting_application" ||
      stage === "persisting_configuration" ||
      stage === "activating_application") &&
    (code === undefined || typeof code === "string")
  );
}

function hasOnlyKeys(value: object, allowed: readonly string[]) {
  const keys = Object.keys(value);
  return keys.every((key) => allowed.includes(key));
}
