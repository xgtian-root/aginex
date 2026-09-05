import { webcrypto } from "node:crypto";
import { beforeAll, describe, expect, it, vi } from "vitest";
import type { UploadSession } from "@/lib/api";
import {
  createTransferLimiter,
  fileResumeFingerprint,
  missingPartNumbers,
  partByteRange,
  retryTransferOperation,
  TRANSFER_RETRY_DELAYS_MS,
} from "./transfer";

const MEBIBYTE = 1024 * 1024;

beforeAll(() => {
  vi.stubGlobal("crypto", webcrypto);
});

describe("file transfer helpers", () => {
  it("fingerprints metadata plus first, middle, and last 1 MiB without MIME", async () => {
    const bytes = new Uint8Array(4 * MEBIBYTE);
    bytes.fill(7);
    const slices: Array<[number, number]> = [];
    const first = fakeFile("archive.bin", bytes, {
      type: "application/octet-stream",
      slices,
    });
    const same = fakeFile("archive.bin", bytes, { type: "text/plain" });
    const changedBytes = bytes.slice();
    changedBytes[2 * MEBIBYTE] = 9;
    const changed = fakeFile("archive.bin", changedBytes);

    await expect(fileResumeFingerprint(first)).resolves.toBe(
      await fileResumeFingerprint(same),
    );
    expect(await fileResumeFingerprint(changed)).not.toBe(
      await fileResumeFingerprint(first),
    );
    expect(slices).toEqual([
      [0, MEBIBYTE],
      [Math.floor(1.5 * MEBIBYTE), Math.floor(2.5 * MEBIBYTE)],
      [3 * MEBIBYTE, 4 * MEBIBYTE],
      [0, MEBIBYTE],
      [Math.floor(1.5 * MEBIBYTE), Math.floor(2.5 * MEBIBYTE)],
      [3 * MEBIBYTE, 4 * MEBIBYTE],
    ]);
  });

  it("retries transient failures three times at 1, 2, and 4 seconds", async () => {
    const operation = vi
      .fn<(attempt: number) => Promise<string>>()
      .mockRejectedValueOnce(statusError(0))
      .mockRejectedValueOnce(statusError(503))
      .mockRejectedValueOnce(new TypeError("connection reset"))
      .mockResolvedValueOnce("uploaded");
    const wait = vi.fn(async (_milliseconds: number) => undefined);

    await expect(retryTransferOperation(operation, { wait })).resolves.toBe(
      "uploaded",
    );

    expect(operation.mock.calls.map(([attempt]) => attempt)).toEqual([
      0, 1, 2, 3,
    ]);
    expect(wait.mock.calls.map(([milliseconds]) => milliseconds)).toEqual(
      TRANSFER_RETRY_DELAYS_MS,
    );
  });

  it("does not retry an explicit client error", async () => {
    const error = statusError(409);
    const operation = vi.fn(async () => {
      throw error;
    });
    const wait = vi.fn(async (_milliseconds: number) => undefined);

    await expect(retryTransferOperation(operation, { wait })).rejects.toBe(
      error,
    );
    expect(operation).toHaveBeenCalledTimes(1);
    expect(wait).not.toHaveBeenCalled();
  });

  it("returns only parts without a durable acknowledgement", () => {
    const session = {
      partCount: 5,
      completedParts: [{ partNumber: 1 }, { partNumber: 4 }],
    } as UploadSession;

    expect(missingPartNumbers(session)).toEqual([2, 3, 5]);
    expect(partByteRange(65, 32, 3)).toEqual({ start: 64, end: 65 });
  });

  it("enforces four shared transfer slots", async () => {
    const limit = createTransferLimiter(4);
    let active = 0;
    let maximum = 0;

    await Promise.all(
      Array.from({ length: 12 }, () =>
        limit(async () => {
          active += 1;
          maximum = Math.max(maximum, active);
          await new Promise((resolve) => setTimeout(resolve, 2));
          active -= 1;
        }),
      ),
    );

    expect(maximum).toBe(4);
  });
});

function fakeFile(
  name: string,
  bytes: Uint8Array,
  options: {
    type?: string;
    lastModified?: number;
    slices?: Array<[number, number]>;
  } = {},
): File {
  return {
    name,
    size: bytes.byteLength,
    lastModified: options.lastModified ?? 1_786_406_400_000,
    type: options.type ?? "application/octet-stream",
    slice(start = 0, end = bytes.byteLength) {
      options.slices?.push([start, end]);
      const selected = bytes.slice(start, end);
      return {
        arrayBuffer: async () => selected.buffer,
      } as Blob;
    },
  } as File;
}

function statusError(status: number) {
  return Object.assign(new Error(`status ${status}`), { status });
}
