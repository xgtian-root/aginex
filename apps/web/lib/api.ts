export type Problem = {
  title: string;
  detail: string;
  status: number;
  requestId?: string;
};

export type User = {
  id: string;
  email: string;
  displayName: string;
  status: string;
  permissions: string[];
  createdAt: string;
};

export type Product = {
  id: string;
  name: string;
  sku: string;
  priceCents: number;
  status: "draft" | "active" | "archived";
  createdAt: string;
  updatedAt: string;
};

export type FileObject = {
  id: string;
  provider: string;
  originalName: string;
  contentType: string;
  size: number;
  visibility: "private" | "public";
  status: "pending" | "ready" | "delete_failed";
  createdAt: string;
};

export type SignedRequest = {
  url: string;
  method: string;
  headers: Record<string, string>;
  expiresAt: string;
};

export type Page<T> = {
  items: T[];
  page: number;
  pageSize: number;
  total: number;
};

export class ApiError extends Error {
  problem: Problem;

  constructor(problem: Problem) {
    super(problem.detail || problem.title);
    this.problem = problem;
  }
}

const apiURL =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1";

export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  if (options.body) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(`${apiURL}${path}`, {
    ...options,
    headers,
    credentials: "include",
  });
  if (!response.ok) {
    let problem: Problem = {
      title: "Request failed",
      detail: `The server returned ${response.status}.`,
      status: response.status,
    };
    try {
      problem = (await response.json()) as Problem;
    } catch {
      // Preserve the useful fallback problem when the response is not JSON.
    }
    throw new ApiError(problem);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}
