import createClient from "openapi-fetch";
import type { components, paths } from "./api.generated";
import { isSetupStatusResponse, isSystemModeResponse } from "./runtime-mode";

export type Problem = components["schemas"]["Problem"];
export type User = components["schemas"]["UserResponse"];
export type Product = components["schemas"]["ProductResponse"];
export type ProductDraft = components["schemas"]["ProductRequest"];
export type ProductPage = components["schemas"]["PageProductResponse"];
export type UserPage = components["schemas"]["PageUserListResponse"];
export type RolePage = components["schemas"]["PageRoleResponse"];
export type AuditLogPage = components["schemas"]["PageAuditLogResponse"];
export type FileObject = components["schemas"]["FileResponse"];
export type FilePage = components["schemas"]["PageFileResponse"];
export type SignedRequest = components["schemas"]["SignedRequestResponse"];
export type PreparedUpload = components["schemas"]["UploadIntentResponse"];
export type UploadIntent = components["schemas"]["UploadIntentRequest"];
export type DashboardSummary =
  components["schemas"]["DashboardSummaryResponse"];
export type SystemMode = components["schemas"]["SystemModeResponse"];
export type SetupStatus = components["schemas"]["SetupStatusResponse"];
export type SetupDatabaseTest =
  components["schemas"]["SetupDatabaseTestRequest"];
export type SetupDatabaseTestResult =
  components["schemas"]["SetupDatabaseTestResponse"];
export type SetupComplete = components["schemas"]["SetupCompleteInput"];
export type SetupAccepted = components["schemas"]["SetupAcceptedResponse"];

type ClientResult<T> = {
  data?: T;
  error?: unknown;
  response: Response;
};

export class ApiError extends Error {
  problem: Problem;
  response: Response;

  constructor(problem: Problem, response: Response) {
    super(problem.detail || problem.title);
    this.problem = problem;
    this.response = response;
  }
}

const configuredURL = process.env.NEXT_PUBLIC_API_URL ?? "";
const apiURL = configuredURL.replace(/\/api\/v1\/?$/, "");
const csrfHeaderName = "X-CSRF-Token" as const;

const client = createClient<paths>({
  baseUrl: apiURL,
  credentials: "include",
});

let csrfRequest: Promise<components["schemas"]["CSRFTokenResponse"]> | null =
  null;

type CSRFHeaderParameter = { "X-CSRF-Token": string };

async function getCSRFToken() {
  const result = await client.GET("/api/v1/auth/csrf");
  return requireData(result);
}

export async function csrfHeaders(): Promise<CSRFHeaderParameter> {
  csrfRequest ??= getCSRFToken();
  try {
    const csrf = await csrfRequest;
    if (csrf.headerName !== csrfHeaderName) {
      throw new Error("The API returned an unexpected CSRF protocol.");
    }
    return { [csrfHeaderName]: csrf.token };
  } catch (error) {
    csrfRequest = null;
    throw error;
  }
}

async function withCSRF<T>(
  operation: (headers: CSRFHeaderParameter) => Promise<ClientResult<T>>,
): Promise<ClientResult<T>> {
  let result = await operation(await csrfHeaders());
  if (!isCSRFForbidden(result)) return result;

  // Another tab can refresh the shared CSRF cookie while this tab still holds
  // an older in-memory token. Clear it and retry the rejected (and therefore
  // side-effect-free) request exactly once with the current cookie token.
  csrfRequest = null;
  result = await operation(await csrfHeaders());
  return result;
}

export async function login(
  body: components["schemas"]["LoginRequest"],
): Promise<User> {
  const result = await withCSRF((header) =>
    client.POST("/api/v1/auth/login", { body, params: { header } }),
  );
  return requireData(result);
}

export async function logout(): Promise<void> {
  const result = await withCSRF((header) =>
    client.POST("/api/v1/auth/logout", { params: { header } }),
  );
  requireSuccess(result);
}

export async function getCurrentUser(): Promise<User> {
  return requireData(await client.GET("/api/v1/auth/me"));
}

export async function getSystemMode(): Promise<SystemMode> {
  const result = await client.GET("/api/v1/system/mode", {
    cache: "no-store",
  });
  const mode = requireData(result);
  if (!isSystemModeResponse(mode)) {
    throw invalidRuntimeResponse(result.response);
  }
  return mode;
}

export async function getSetupStatus(): Promise<SetupStatus> {
  const result = await client.GET("/api/v1/setup/status", {
    cache: "no-store",
  });
  const status = requireData(result);
  if (!isSetupStatusResponse(status)) {
    throw invalidRuntimeResponse(result.response);
  }
  return status;
}

export async function testSetupDatabase(
  body: SetupDatabaseTest,
): Promise<SetupDatabaseTestResult> {
  return requireData(
    await withCSRF((header) =>
      client.POST("/api/v1/setup/database/test", {
        body,
        cache: "no-store",
        params: { header },
      }),
    ),
  );
}

export async function completeSetup(
  body: SetupComplete,
): Promise<SetupAccepted> {
  return requireData(
    await withCSRF((header) =>
      client.POST("/api/v1/setup/complete", {
        body,
        cache: "no-store",
        params: { header },
      }),
    ),
  );
}

export async function getDashboardSummary(): Promise<DashboardSummary> {
  return requireData(await client.GET("/api/v1/dashboard/summary"));
}

export async function listProducts(search = ""): Promise<ProductPage> {
  return requireData(
    await client.GET("/api/v1/products", {
      params: { query: { search } },
    }),
  );
}

export async function createProduct(body: ProductDraft): Promise<Product> {
  const key = idempotencyKey();
  return requireData(
    await withCSRF((headers) =>
      client.POST("/api/v1/products", {
        body,
        params: {
          header: {
            ...headers,
            "Idempotency-Key": key,
          },
        },
      }),
    ),
  );
}

export async function deleteProduct(id: string): Promise<void> {
  const key = idempotencyKey();
  const result = await withCSRF((headers) =>
    client.DELETE("/api/v1/products/{id}", {
      params: {
        path: { id },
        header: {
          ...headers,
          "Idempotency-Key": key,
        },
      },
    }),
  );
  requireSuccess(result);
}

export async function listUsers(): Promise<UserPage> {
  return requireData(await client.GET("/api/v1/users"));
}

export async function listRoles(): Promise<RolePage> {
  return requireData(await client.GET("/api/v1/roles"));
}

export async function listAuditLogs(): Promise<AuditLogPage> {
  return requireData(await client.GET("/api/v1/audit-logs"));
}

export async function listFiles(): Promise<FilePage> {
  return requireData(await client.GET("/api/v1/files"));
}

export async function createUploadIntent(
  body: UploadIntent,
): Promise<PreparedUpload> {
  const key = idempotencyKey();
  return requireData(
    await withCSRF((headers) =>
      client.POST("/api/v1/files/upload-intents", {
        body,
        params: {
          header: {
            ...headers,
            "Idempotency-Key": key,
          },
        },
      }),
    ),
  );
}

export async function confirmUpload(id: string): Promise<FileObject> {
  const key = idempotencyKey();
  return requireData(
    await withCSRF((headers) =>
      client.POST("/api/v1/files/{id}/confirm", {
        params: {
          path: { id },
          header: {
            ...headers,
            "Idempotency-Key": key,
          },
        },
      }),
    ),
  );
}

export async function getFileURL(id: string): Promise<SignedRequest> {
  return requireData(
    await client.GET("/api/v1/files/{id}/url", {
      params: { path: { id } },
    }),
  );
}

export async function deleteFile(id: string): Promise<void> {
  const key = idempotencyKey();
  const result = await withCSRF((headers) =>
    client.DELETE("/api/v1/files/{id}", {
      params: {
        path: { id },
        header: {
          ...headers,
          "Idempotency-Key": key,
        },
      },
    }),
  );
  requireSuccess(result);
}

function requireData<T>(result: ClientResult<T>): T {
  if (!result.response.ok || result.error !== undefined) {
    throw new ApiError(
      problemFrom(result.error, result.response),
      result.response,
    );
  }
  if (result.data === undefined) {
    throw new ApiError(
      fallbackProblem(result.response, "The API returned no response body."),
      result.response,
    );
  }
  return result.data;
}

function requireSuccess(result: ClientResult<unknown>): void {
  if (!result.response.ok || result.error !== undefined) {
    throw new ApiError(
      problemFrom(result.error, result.response),
      result.response,
    );
  }
}

function isCSRFForbidden(result: ClientResult<unknown>): boolean {
  return (
    result.response.status === 403 &&
    problemFrom(result.error, result.response).code === "CSRF_FORBIDDEN"
  );
}

function problemFrom(error: unknown, response: Response): Problem {
  if (
    typeof error === "object" &&
    error !== null &&
    "title" in error &&
    "detail" in error &&
    "status" in error &&
    "code" in error
  ) {
    return error as Problem;
  }
  return fallbackProblem(response, `The server returned ${response.status}.`);
}

function fallbackProblem(response: Response, detail: string): Problem {
  return {
    type: "about:blank",
    title: "Request failed",
    detail,
    status: response.status,
    instance: response.url || "",
    code: "REQUEST_FAILED",
    requestId: response.headers.get("X-Request-ID") ?? "",
  };
}

function invalidRuntimeResponse(response: Response): ApiError {
  return new ApiError(
    fallbackProblem(
      response,
      "The API returned an invalid runtime state response.",
    ),
    response,
  );
}

function idempotencyKey(): string {
  return crypto.randomUUID();
}
