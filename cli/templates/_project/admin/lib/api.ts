import createClient from "openapi-fetch";
import type { components, paths } from "./api.generated";
import { isSetupStatusResponse, isSystemModeResponse } from "./runtime-mode";

export type Problem = components["schemas"]["Problem"];
export type User = components["schemas"]["UserResponse"];
export type Product = components["schemas"]["ProductResponse"];
export type ProductDraft = components["schemas"]["ProductRequest"];
export type ProductPage = components["schemas"]["PageProductResponse"];
export type UserDetail = components["schemas"]["UserResponse"];
export type UserPage = components["schemas"]["PageUserListResponse"];
export type UserDraft = components["schemas"]["CreateUserRequest"];
export type UserUpdate = components["schemas"]["UpdateUserRequest"];
export type UserRolesUpdate = components["schemas"]["ReplaceUserRolesRequest"];
export type UserPasswordUpdate =
  components["schemas"]["ResetUserPasswordRequest"];
export type RolePage = components["schemas"]["PageRoleResponse"];
export type Role = components["schemas"]["RoleResponse"];
export type RoleDraft = components["schemas"]["CreateRoleRequest"];
export type RoleUpdate = components["schemas"]["UpdateRoleRequest"];
export type RoleGrantsUpdate =
  components["schemas"]["ReplaceRoleGrantsRequest"];
export type Permission = components["schemas"]["PermissionResponse"];
export type PermissionPage = components["schemas"]["PagePermissionResponse"];
export type AuditLogPage = components["schemas"]["PageAuditLogResponse"];
export type FileObject = components["schemas"]["FileResponse"];
export type FilePage = components["schemas"]["PageFileResponse"];
export type SignedRequest = components["schemas"]["SignedRequestResponse"];
export type UploadStrategy = "single" | "resumable";
export type UploadIntent = {
  filename: string;
  contentType: string;
  size: number;
  visibility: "private" | "public";
  strategy: UploadStrategy;
  resumeFingerprint?: string;
};
export type PreparedUpload = {
  strategy: UploadStrategy;
  file: FileObject;
  upload?: SignedRequest;
  session?: UploadSession;
};
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
export type FileUploadPolicy = {
  maxUploadBytes: number;
  resumableUploadsEnabled: boolean;
};
export type RuntimeFileUploadPolicy = FileUploadPolicy & {
  resumableAvailable: boolean;
  multipartThresholdBytes: number;
  multipartPartSizeBytes: number;
  sessionTtlSeconds: number;
  maxBatchFiles: number;
};
export type UploadPart = {
  partNumber: number;
  size: number;
  confirmedAt: string;
};
export type UploadSession = {
  id: string;
  file: FileObject;
  status:
    | "active"
    | "completing"
    | "verifying"
    | "completed"
    | "cancelling"
    | "cancelled"
    | "expiring"
    | "expired";
  partSize: number;
  partCount: number;
  completedParts: UploadPart[];
  expiresAt: string;
  createdAt: string;
  updatedAt: string;
};
export type UploadSessionPage = {
  items: UploadSession[];
  page: number;
  pageSize: number;
  total: number;
};
export type SignedUploadPart = {
  partNumber: number;
  size: number;
  upload: SignedRequest;
};
export type StorageSettings =
  components["schemas"]["StorageSettingsResponse"] & {
    runtimeFileUploadPolicy: FileUploadPolicy;
    pendingFileUploadPolicy: FileUploadPolicy;
  };
export type StorageProfile = components["schemas"]["StorageProfileResponse"];
export type StorageProfilePage =
  components["schemas"]["PageStorageProfileResponse"];
export type StorageProfileDraft =
  components["schemas"]["StorageProfileRequest"];
export type StorageProfileTest =
  components["schemas"]["StorageProfileTestResponse"];

export type Revisioned<T> = { value: T; etag: string };

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
type IdempotentCSRFHeaderParameter = CSRFHeaderParameter & {
  "Idempotency-Key": string;
};

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

async function withIdempotentCSRF<T>(
  operation: (
    headers: IdempotentCSRFHeaderParameter,
  ) => Promise<ClientResult<T>>,
): Promise<ClientResult<T>> {
  const key = idempotencyKey();
  return withCSRF((headers) =>
    operation({ ...headers, "Idempotency-Key": key }),
  );
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

export type UserListParameters = {
  search?: string;
  status?: "active" | "disabled";
  roleId?: string;
  page?: number;
  pageSize?: number;
};

export async function listUsers(
  parameters: UserListParameters = {},
): Promise<UserPage> {
  return requireData(
    await client.GET("/api/v1/users", {
      params: { query: parameters },
    }),
  );
}

export async function getUser(id: string): Promise<UserDetail> {
  return requireData(
    await client.GET("/api/v1/users/{id}", { params: { path: { id } } }),
  );
}

export async function createUser(body: UserDraft): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.POST("/api/v1/users", {
        body,
        params: { header },
      }),
    ),
  );
}

export async function updateUser(
  id: string,
  body: UserUpdate,
): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.PUT("/api/v1/users/{id}", {
        body,
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function replaceUserRoles(
  id: string,
  body: UserRolesUpdate,
): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.PUT("/api/v1/users/{id}/roles", {
        body,
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function enableUser(id: string): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.POST("/api/v1/users/{id}/enable", {
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function disableUser(id: string): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.POST("/api/v1/users/{id}/disable", {
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function resetUserPassword(
  id: string,
  body: UserPasswordUpdate,
): Promise<void> {
  const result = await withIdempotentCSRF((header) =>
    client.PUT("/api/v1/users/{id}/password", {
      body,
      params: { path: { id }, header },
    }),
  );
  requireSuccess(result);
}

export async function grantUserAdministrator(id: string): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.PUT("/api/v1/users/{id}/administrator", {
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function revokeUserAdministrator(id: string): Promise<UserDetail> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.DELETE("/api/v1/users/{id}/administrator", {
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function deleteUser(id: string): Promise<void> {
  const result = await withIdempotentCSRF((header) =>
    client.DELETE("/api/v1/users/{id}", {
      params: { path: { id }, header },
    }),
  );
  requireSuccess(result);
}

export type RoleListParameters = {
  search?: string;
  page?: number;
  pageSize?: number;
};

export async function listRoles(
  parameters: RoleListParameters = {},
): Promise<RolePage> {
  return requireData(
    await client.GET("/api/v1/roles", {
      params: { query: parameters },
    }),
  );
}

export async function listRoleCatalog(): Promise<Role[]> {
  return listCatalog((page, pageSize) => listRoles({ page, pageSize }));
}

export async function getRole(id: string): Promise<Role> {
  return requireData(
    await client.GET("/api/v1/roles/{id}", { params: { path: { id } } }),
  );
}

export async function createRole(body: RoleDraft): Promise<Role> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.POST("/api/v1/roles", { body, params: { header } }),
    ),
  );
}

export async function updateRole(id: string, body: RoleUpdate): Promise<Role> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.PUT("/api/v1/roles/{id}", {
        body,
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function replaceRoleGrants(
  id: string,
  body: RoleGrantsUpdate,
): Promise<Role> {
  return requireData(
    await withIdempotentCSRF((header) =>
      client.PUT("/api/v1/roles/{id}/grants", {
        body,
        params: { path: { id }, header },
      }),
    ),
  );
}

export async function deleteRole(id: string): Promise<void> {
  const result = await withIdempotentCSRF((header) =>
    client.DELETE("/api/v1/roles/{id}", {
      params: { path: { id }, header },
    }),
  );
  requireSuccess(result);
}

export type PermissionListParameters = {
  search?: string;
  page?: number;
  pageSize?: number;
};

export async function listPermissions(
  parameters: PermissionListParameters = {},
): Promise<PermissionPage> {
  return requireData(
    await client.GET("/api/v1/permissions", {
      params: { query: parameters },
    }),
  );
}

export async function listPermissionCatalog(): Promise<Permission[]> {
  return listCatalog((page, pageSize) => listPermissions({ page, pageSize }));
}

export async function listAuditLogs(): Promise<AuditLogPage> {
  return requireData(await client.GET("/api/v1/audit-logs"));
}

export async function listFiles(): Promise<FilePage> {
  return requireData(await client.GET("/api/v1/files"));
}

export async function createUploadIntent(
  body: UploadIntent,
  requestKey = idempotencyKey(),
): Promise<PreparedUpload> {
  return requireData(
    await withCSRF((headers) =>
      jsonRequest<PreparedUpload>("/api/v1/files/upload-intents", {
        method: "POST",
        headers: {
          ...headers,
          "Content-Type": "application/json",
          "Idempotency-Key": requestKey,
        },
        body: JSON.stringify(body),
      }),
    ),
  );
}

export async function getFileUploadPolicy(): Promise<RuntimeFileUploadPolicy> {
  return requireData(
    await jsonRequest<RuntimeFileUploadPolicy>("/api/v1/files/upload-policy"),
  );
}

export class ObjectUploadError extends Error {
  constructor(readonly status: number) {
    super(`Object upload returned ${status}.`);
    this.name = "ObjectUploadError";
  }
}

export async function uploadPreparedFile(
  prepared: PreparedUpload,
  file: File,
  onProgress: (percentage: number) => void,
  signal?: AbortSignal,
): Promise<void> {
  if (prepared.strategy !== "single" || !prepared.upload) {
    throw new Error("The API did not return a single-upload request.");
  }

  await uploadSignedBlob(prepared.upload, file, onProgress, signal);
}

export async function uploadSignedBlob(
  upload: SignedRequest,
  content: Blob,
  onProgress: (percentage: number) => void,
  signal?: AbortSignal,
): Promise<string | null> {
  if (signal?.aborted) throw new DOMException("Upload aborted", "AbortError");
  const localUpload =
    upload.url.includes("/api/v1/files/local-upload/") ||
    upload.url.includes("/api/v1/files/upload-sessions/");
  const headers = new Headers(upload.headers);
  if (localUpload) {
    for (const [name, value] of Object.entries(await csrfHeaders())) {
      headers.set(name, value);
    }
  }
  if (signal?.aborted) throw new DOMException("Upload aborted", "AbortError");

  return new Promise<string | null>((resolve, reject) => {
    const request = new XMLHttpRequest();
    const cleanup = () => signal?.removeEventListener("abort", abort);
    const abort = () => request.abort();
    request.open(upload.method, upload.url);
    request.withCredentials = localUpload;
    for (const [name, value] of headers) request.setRequestHeader(name, value);
    request.upload.addEventListener("progress", (event) => {
      if (!event.lengthComputable || event.total === 0) return;
      onProgress(Math.min(99, Math.round((event.loaded / event.total) * 100)));
    });
    request.addEventListener("load", () => {
      if (request.status >= 200 && request.status < 300) {
        cleanup();
        onProgress(100);
        resolve(request.getResponseHeader("ETag"));
      } else {
        cleanup();
        reject(new ObjectUploadError(request.status));
      }
    });
    request.addEventListener("error", () => {
      cleanup();
      reject(new ObjectUploadError(0));
    });
    request.addEventListener("abort", () => {
      cleanup();
      reject(new DOMException("Upload aborted", "AbortError"));
    });
    signal?.addEventListener("abort", abort, { once: true });
    request.send(content);
  });
}

export async function listIncompleteUploadSessions(): Promise<UploadSessionPage> {
  return requireData(
    await jsonRequest<UploadSessionPage>(
      "/api/v1/files/upload-sessions?state=incomplete",
    ),
  );
}

export async function getUploadSession(id: string): Promise<UploadSession> {
  return requireData(
    await jsonRequest<UploadSession>(
      `/api/v1/files/upload-sessions/${encodeURIComponent(id)}`,
    ),
  );
}

export async function resumeUploadSession(
  id: string,
  fingerprint: string,
): Promise<UploadSession> {
  return requireData(
    await withIdempotentCSRF((headers) =>
      jsonRequest<UploadSession>(
        `/api/v1/files/upload-sessions/${encodeURIComponent(id)}/resume`,
        {
          method: "POST",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ fingerprint }),
        },
      ),
    ),
  );
}

export async function signUploadParts(
  id: string,
  partNumbers: number[],
): Promise<SignedUploadPart[]> {
  const response = requireData(
    await withCSRF((headers) =>
      jsonRequest<{ items: SignedUploadPart[] }>(
        `/api/v1/files/upload-sessions/${encodeURIComponent(id)}/parts/sign`,
        {
          method: "POST",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ partNumbers }),
        },
      ),
    ),
  );
  return response.items;
}

export async function acknowledgeUploadParts(
  id: string,
  parts: Array<{ partNumber: number; etag: string }>,
): Promise<UploadSession> {
  return requireData(
    await withIdempotentCSRF((headers) =>
      jsonRequest<UploadSession>(
        `/api/v1/files/upload-sessions/${encodeURIComponent(id)}/parts/ack`,
        {
          method: "POST",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ parts }),
        },
      ),
    ),
  );
}

export async function completeUploadSession(id: string): Promise<FileObject> {
  return requireData(
    await withIdempotentCSRF((headers) =>
      jsonRequest<FileObject>(
        `/api/v1/files/upload-sessions/${encodeURIComponent(id)}/complete`,
        { method: "POST", headers },
      ),
    ),
  );
}

export async function cancelUploadSession(id: string): Promise<void> {
  const result = await withIdempotentCSRF((headers) =>
    jsonRequest<void>(
      `/api/v1/files/upload-sessions/${encodeURIComponent(id)}`,
      { method: "DELETE", headers },
    ),
  );
  requireSuccess(result);
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

export async function getFileURL(
  id: string,
  purpose?: "preview" | "download",
): Promise<SignedRequest> {
  const query = purpose ? `?${new URLSearchParams({ purpose })}` : "";
  return requireData(
    await jsonRequest<SignedRequest>(
      `/api/v1/files/${encodeURIComponent(id)}/url${query}`,
    ),
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

function revisioned<T>(result: ClientResult<T>): Revisioned<T> {
  const value = requireData(result);
  const etag = result.response.headers.get("ETag");
  if (!etag) {
    throw new ApiError(
      fallbackProblem(result.response, "The API returned no storage revision."),
      result.response,
    );
  }
  return { value, etag };
}

export async function getStorageSettings(): Promise<
  Revisioned<StorageSettings>
> {
  return revisioned(
    (await client.GET(
      "/api/v1/storage-settings",
    )) as ClientResult<StorageSettings>,
  );
}

export async function updateFileUploadPolicy(
  body: FileUploadPolicy,
  etag: string,
): Promise<Revisioned<StorageSettings>> {
  return revisioned(
    await withIdempotentCSRF((headers) =>
      jsonRequest<StorageSettings>(
        "/api/v1/storage-settings/file-upload-policy",
        {
          method: "PUT",
          headers: {
            ...headers,
            "Content-Type": "application/json",
            "If-Match": etag,
          },
          body: JSON.stringify(body),
        },
      ),
    ),
  );
}

export async function listStorageProfiles(
  page = 1,
  pageSize = 100,
): Promise<Revisioned<StorageProfilePage>> {
  return revisioned(
    await client.GET("/api/v1/storage-profiles", {
      params: { query: { page, pageSize } },
    }),
  );
}

export async function createStorageProfile(
  body: StorageProfileDraft,
  etag: string,
): Promise<Revisioned<StorageProfile>> {
  const result = await withIdempotentCSRF((headers) =>
    client.POST("/api/v1/storage-profiles", {
      body,
      params: { header: { ...headers, "If-Match": etag } },
    }),
  );
  return revisioned(result);
}

export async function updateStorageProfile(
  id: string,
  body: StorageProfileDraft,
  etag: string,
): Promise<Revisioned<StorageProfile>> {
  const result = await withIdempotentCSRF((headers) =>
    client.PUT("/api/v1/storage-profiles/{id}", {
      body,
      params: { path: { id }, header: { ...headers, "If-Match": etag } },
    }),
  );
  return revisioned(result);
}

export async function testStorageProfile(
  body: StorageProfileDraft,
): Promise<StorageProfileTest> {
  return requireData(
    await withIdempotentCSRF((headers) =>
      client.POST("/api/v1/storage-profiles/test", {
        body,
        params: { header: headers },
      }),
    ),
  );
}

async function storageProfileTransition<T>(
  path:
    | "/api/v1/storage-profiles/{id}/activate"
    | "/api/v1/storage-profiles/{id}/archive"
    | "/api/v1/storage-profiles/{id}/restore",
  id: string,
  etag: string,
): Promise<Revisioned<T>> {
  const result = await withIdempotentCSRF((headers) =>
    client.POST(path, {
      params: { path: { id }, header: { ...headers, "If-Match": etag } },
    }),
  );
  return revisioned(result as ClientResult<T>);
}

export function activateStorageProfile(id: string, etag: string) {
  return storageProfileTransition<StorageSettings>(
    "/api/v1/storage-profiles/{id}/activate",
    id,
    etag,
  );
}

export function archiveStorageProfile(id: string, etag: string) {
  return storageProfileTransition<StorageProfile>(
    "/api/v1/storage-profiles/{id}/archive",
    id,
    etag,
  );
}

export function restoreStorageProfile(id: string, etag: string) {
  return storageProfileTransition<StorageProfile>(
    "/api/v1/storage-profiles/{id}/restore",
    id,
    etag,
  );
}

export async function deleteStorageProfile(
  id: string,
  etag: string,
): Promise<void> {
  const result = await withIdempotentCSRF((headers) =>
    client.DELETE("/api/v1/storage-profiles/{id}", {
      params: { path: { id }, header: { ...headers, "If-Match": etag } },
    }),
  );
  requireSuccess(result);
}

async function jsonRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<ClientResult<T>> {
  const response = await fetch(`${apiURL}${path}`, {
    ...init,
    credentials: "include",
  });
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    payload = undefined;
  }
  return response.ok
    ? { data: payload as T | undefined, response }
    : { error: payload, response };
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

async function listCatalog<T extends { id: string }>(
  fetchPage: (
    page: number,
    pageSize: number,
  ) => Promise<{ items: T[]; pageSize: number; total: number }>,
): Promise<T[]> {
  const requestedPageSize = 100;
  const first = await fetchPage(1, requestedPageSize);
  const totalPages = Math.ceil(first.total / Math.max(1, first.pageSize));
  const remaining = await Promise.all(
    Array.from({ length: Math.max(0, totalPages - 1) }, (_, index) =>
      fetchPage(index + 2, requestedPageSize),
    ),
  );
  const uniqueItems = new Map<string, T>();
  for (const item of [first, ...remaining].flatMap((result) => result.items)) {
    uniqueItems.set(item.id, item);
  }
  return [...uniqueItems.values()];
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
