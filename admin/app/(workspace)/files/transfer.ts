import type { UploadSession } from "@/lib/api";

const FINGERPRINT_SAMPLE_BYTES = 1024 * 1024;
export const TRANSFER_RETRY_DELAYS_MS = [1000, 2000, 4000] as const;

export type TransferLimiter = <T>(operation: () => Promise<T>) => Promise<T>;

type RetryOptions = {
  signal?: AbortSignal;
  wait?: (milliseconds: number, signal?: AbortSignal) => Promise<void>;
};

export function createTransferLimiter(maximum: number): TransferLimiter {
  let active = 0;
  const waiting: Array<() => void> = [];

  const acquire = async () => {
    if (active < maximum) {
      active += 1;
      return;
    }
    await new Promise<void>((resolve) => waiting.push(resolve));
    active += 1;
  };

  const release = () => {
    active -= 1;
    waiting.shift()?.();
  };

  return async <T>(operation: () => Promise<T>) => {
    await acquire();
    try {
      return await operation();
    } finally {
      release();
    }
  };
}

export async function fileResumeFingerprint(file: File): Promise<string> {
  const metadata = new TextEncoder().encode(
    `${file.name}\0${file.size}\0${file.lastModified}\0`,
  );
  const middleStart = Math.max(
    0,
    Math.floor((file.size - FINGERPRINT_SAMPLE_BYTES) / 2),
  );
  const first = new Uint8Array(
    await file.slice(0, FINGERPRINT_SAMPLE_BYTES).arrayBuffer(),
  );
  const middle = new Uint8Array(
    await file
      .slice(middleStart, middleStart + FINGERPRINT_SAMPLE_BYTES)
      .arrayBuffer(),
  );
  const last = new Uint8Array(
    await file
      .slice(Math.max(0, file.size - FINGERPRINT_SAMPLE_BYTES), file.size)
      .arrayBuffer(),
  );
  const input = new Uint8Array(
    metadata.length + first.length + middle.length + last.length,
  );
  input.set(metadata);
  input.set(first, metadata.length);
  input.set(middle, metadata.length + first.length);
  input.set(last, metadata.length + first.length + middle.length);
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", input));
  return Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}

export async function retryTransferOperation<T>(
  operation: (attempt: number) => Promise<T>,
  options: RetryOptions = {},
): Promise<T> {
  const wait = options.wait ?? waitForRetry;
  for (let attempt = 0; ; attempt += 1) {
    throwIfAborted(options.signal);
    try {
      return await operation(attempt);
    } catch (error) {
      if (
        attempt >= TRANSFER_RETRY_DELAYS_MS.length ||
        !isRetryableTransferError(error)
      ) {
        throw error;
      }
      await wait(TRANSFER_RETRY_DELAYS_MS[attempt], options.signal);
    }
  }
}

export function isRetryableTransferError(error: unknown): boolean {
  if (isAbortError(error)) return false;
  const status = transferErrorStatus(error);
  if (status !== undefined) {
    return status === 0 || status >= 500;
  }
  return error instanceof TypeError || error instanceof Error;
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException
    ? error.name === "AbortError"
    : typeof error === "object" &&
        error !== null &&
        "name" in error &&
        error.name === "AbortError";
}

function transferErrorStatus(error: unknown): number | undefined {
  if (typeof error !== "object" || error === null) return undefined;
  if ("status" in error && typeof error.status === "number") {
    return error.status;
  }
  if ("response" in error && error.response instanceof Response) {
    return error.response.status;
  }
  return undefined;
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw abortError();
}

function waitForRetry(milliseconds: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    throwIfAborted(signal);
    const timeout = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    const onAbort = () => {
      clearTimeout(timeout);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function abortError() {
  return new DOMException("Transfer aborted", "AbortError");
}

export function missingPartNumbers(session: UploadSession): number[] {
  const complete = new Set(
    session.completedParts.map((part) => part.partNumber),
  );
  return Array.from(
    { length: session.partCount },
    (_, index) => index + 1,
  ).filter((partNumber) => !complete.has(partNumber));
}

export function partByteRange(
  fileSize: number,
  partSize: number,
  partNumber: number,
) {
  const start = (partNumber - 1) * partSize;
  return { start, end: Math.min(fileSize, start + partSize) };
}

export function completedSessionBytes(session: UploadSession): number {
  return session.completedParts.reduce((total, part) => total + part.size, 0);
}
