// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import messages from "../../../messages/en.json";
import FilesPage from "./page";

const mocks = vi.hoisted(() => ({
  acknowledgeUploadParts: vi.fn(),
  completeUploadSession: vi.fn(),
  confirmUpload: vi.fn(),
  createUploadIntent: vi.fn(),
  deleteFile: vi.fn(),
  getCurrentUser: vi.fn(),
  getFileUploadPolicy: vi.fn(),
  getFileURL: vi.fn(),
  listIncompleteUploadSessions: vi.fn(),
  listFiles: vi.fn(),
  resumeFingerprint: vi.fn(),
  resumeUploadSession: vi.fn(),
  signUploadParts: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
  uploadPreparedFile: vi.fn(),
  uploadSignedBlob: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  ObjectUploadError: class ObjectUploadError extends Error {
    constructor(readonly status: number) {
      super();
    }
  },
  acknowledgeUploadParts: mocks.acknowledgeUploadParts,
  completeUploadSession: mocks.completeUploadSession,
  confirmUpload: mocks.confirmUpload,
  createUploadIntent: mocks.createUploadIntent,
  deleteFile: mocks.deleteFile,
  getCurrentUser: mocks.getCurrentUser,
  getFileUploadPolicy: mocks.getFileUploadPolicy,
  getFileURL: mocks.getFileURL,
  listIncompleteUploadSessions: mocks.listIncompleteUploadSessions,
  listFiles: mocks.listFiles,
  resumeUploadSession: mocks.resumeUploadSession,
  signUploadParts: mocks.signUploadParts,
  uploadPreparedFile: mocks.uploadPreparedFile,
  uploadSignedBlob: mocks.uploadSignedBlob,
}));

vi.mock("./transfer", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./transfer")>();
  return {
    ...actual,
    fileResumeFingerprint: mocks.resumeFingerprint,
    retryTransferOperation: (
      operation: (attempt: number) => Promise<unknown>,
      options: { signal?: AbortSignal } = {},
    ) =>
      actual.retryTransferOperation(operation, {
        ...options,
        wait: async () => undefined,
      }),
  };
});

vi.mock("sonner", () => ({
  toast: { error: mocks.toastError, success: mocks.toastSuccess },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getCurrentUser.mockResolvedValue({
    grants: [
      { permission: "files:read", scope: "own" },
      { permission: "files:create", scope: "own" },
      { permission: "files:delete", scope: "own" },
    ],
  });
  mocks.getFileUploadPolicy.mockResolvedValue({
    maxUploadBytes: 10 * 1024 * 1024,
    resumableUploadsEnabled: false,
    resumableAvailable: false,
    multipartThresholdBytes: 32 * 1024 * 1024,
    multipartPartSizeBytes: 32 * 1024 * 1024,
    sessionTtlSeconds: 86_400,
    maxBatchFiles: 20,
  });
  mocks.listIncompleteUploadSessions.mockResolvedValue({
    items: [],
    page: 1,
    pageSize: 20,
    total: 0,
  });
  mocks.listFiles.mockResolvedValue({
    items: [],
    page: 1,
    pageSize: 20,
    total: 0,
  });
  mocks.createUploadIntent.mockImplementation(async ({ filename }) => ({
    strategy: "single",
    file: { id: filename },
    upload: { method: "PUT", url: "/upload", headers: {} },
  }));
  mocks.uploadPreparedFile.mockImplementation(
    async (_prepared, _file, progress) => {
      progress(42);
      progress(100);
    },
  );
  mocks.confirmUpload.mockResolvedValue({});
  mocks.completeUploadSession.mockResolvedValue({});
  mocks.resumeFingerprint.mockResolvedValue("a".repeat(64));
  mocks.uploadSignedBlob.mockResolvedValue('"etag"');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("FilesPage transfer workbench", () => {
  it("stages multiple arbitrary files and waits for explicit confirmation", async () => {
    renderPage();
    const input = await findFileInput();
    const files = [
      new File(["hello"], "notes.txt", { type: "text/plain" }),
      new File(["data"], "archive.bin", {
        type: "application/octet-stream",
      }),
    ];

    fireEvent.change(input, { target: { files } });

    const start = await screen.findByRole("button", {
      name: "Start 2 transfers",
    });
    expect(mocks.createUploadIntent).not.toHaveBeenCalled();
    expect(screen.getByText("notes.txt")).toBeInTheDocument();
    expect(screen.getByText("archive.bin")).toBeInTheDocument();

    fireEvent.click(start);

    await waitFor(() => expect(mocks.confirmUpload).toHaveBeenCalledTimes(2));
    expect(mocks.createUploadIntent).toHaveBeenCalledWith(
      expect.objectContaining({
        contentType: "text/plain",
        strategy: "single",
        visibility: "private",
      }),
      expect.any(String),
    );
    expect(await screen.findAllByText("Verified")).toHaveLength(2);
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "2 files uploaded and verified.",
    );
  });

  it("caps a staged batch at twenty files", async () => {
    renderPage();
    const input = await findFileInput();
    const files = Array.from(
      { length: 21 },
      (_, index) => new File([String(index)], `file-${index}.dat`),
    );

    fireEvent.change(input, { target: { files } });

    expect(
      await screen.findByRole("button", { name: "Start 20 transfers" }),
    ).toBeEnabled();
    expect(screen.getAllByRole("listitem")).toHaveLength(20);
    expect(mocks.toastError).toHaveBeenCalledWith(
      "A transfer batch can contain at most 20 files.",
    );
  });

  it("keeps an empty file out of the runnable queue", async () => {
    renderPage();
    fireEvent.change(await findFileInput(), {
      target: { files: [new File([], "empty.txt", { type: "text/plain" })] },
    });

    expect(
      await screen.findByText("Empty files cannot be uploaded."),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Start 0 transfers" }),
    ).toBeDisabled();
    expect(mocks.createUploadIntent).not.toHaveBeenCalled();
  });

  it("runs at most four file transfers concurrently", async () => {
    let active = 0;
    let maximum = 0;
    mocks.uploadPreparedFile.mockImplementation(async () => {
      active += 1;
      maximum = Math.max(maximum, active);
      await new Promise((resolve) => setTimeout(resolve, 5));
      active -= 1;
    });
    renderPage();
    const input = await findFileInput();
    const files = Array.from(
      { length: 8 },
      (_, index) => new File([String(index)], `file-${index}.dat`),
    );
    fireEvent.change(input, { target: { files } });

    fireEvent.click(
      await screen.findByRole("button", { name: "Start 8 transfers" }),
    );

    await waitFor(() => expect(mocks.confirmUpload).toHaveBeenCalledTimes(8));
    expect(maximum).toBe(4);
  });

  it("creates a fresh direct-upload intent after a transient failure", async () => {
    mocks.uploadPreparedFile
      .mockRejectedValueOnce(statusError(0))
      .mockImplementationOnce(async (_prepared, _file, progress) => {
        progress(100);
      });
    renderPage();
    fireEvent.change(await findFileInput(), {
      target: { files: [new File(["report"], "report.csv")] },
    });

    fireEvent.click(
      await screen.findByRole("button", { name: "Start 1 transfers" }),
    );

    await waitFor(() => expect(mocks.confirmUpload).toHaveBeenCalledTimes(1));
    expect(mocks.createUploadIntent).toHaveBeenCalledTimes(2);
    const firstKey = mocks.createUploadIntent.mock.calls[0]?.[1];
    const secondKey = mocks.createUploadIntent.mock.calls[1]?.[1];
    expect(firstKey).toEqual(expect.any(String));
    expect(secondKey).toEqual(expect.any(String));
    expect(secondKey).not.toBe(firstKey);
  });

  it("re-signs a transiently failed part and acknowledges two parts together", async () => {
    mocks.getFileUploadPolicy.mockResolvedValue(resumablePolicy());
    let current = uploadSession("large.bin", 33 * 1024 * 1024, 2);
    mocks.createUploadIntent.mockResolvedValue({
      strategy: "resumable",
      file: current.file,
      session: current,
    });
    mocks.signUploadParts.mockImplementation(async (_id, partNumbers) =>
      partNumbers.map((partNumber: number) => ({
        partNumber,
        size: 32 * 1024 * 1024,
        upload: {
          method: "PUT",
          url: `/upload/${partNumber}`,
          headers: {},
        },
      })),
    );
    let partOneAttempts = 0;
    mocks.uploadSignedBlob.mockImplementation(
      async (upload, _content, progress) => {
        if (upload.url.endsWith("/1") && partOneAttempts++ === 0) {
          throw statusError(0);
        }
        progress(100);
        return `"etag-${upload.url.at(-1)}"`;
      },
    );
    mocks.acknowledgeUploadParts.mockImplementation(async (_id, parts) => {
      current = {
        ...current,
        completedParts: parts.map((part: { partNumber: number }) => ({
          partNumber: part.partNumber,
          size: part.partNumber === 1 ? 32 * 1024 * 1024 : 1024 * 1024,
          confirmedAt: "2026-08-11T00:00:00Z",
        })),
      };
      return current;
    });
    renderPage();
    const file = sizedFile("large.bin", 33 * 1024 * 1024);
    fireEvent.change(await findFileInput(), { target: { files: [file] } });

    fireEvent.click(
      await screen.findByRole("button", { name: "Start 1 transfers" }),
    );

    await waitFor(() =>
      expect(mocks.completeUploadSession).toHaveBeenCalledWith(current.id),
    );
    expect(mocks.createUploadIntent).toHaveBeenCalledWith(
      expect.objectContaining({
        strategy: "resumable",
        resumeFingerprint: "a".repeat(64),
      }),
      expect.any(String),
    );
    expect(
      mocks.signUploadParts.mock.calls.filter(
        ([id, partNumbers]) => id === current.id && partNumbers[0] === 1,
      ),
    ).toHaveLength(2);
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [2]);
    expect(mocks.acknowledgeUploadParts.mock.calls[0]?.[1]).toHaveLength(2);
    expect(mocks.confirmUpload).not.toHaveBeenCalled();
  });

  it("immediately aborts active parts, rolls back, then continues", async () => {
    mocks.getFileUploadPolicy.mockResolvedValue(resumablePolicy());
    let current = uploadSession("movie.bin", 65 * 1024 * 1024, 3);
    mocks.createUploadIntent.mockResolvedValue({
      strategy: "resumable",
      file: current.file,
      session: current,
    });
    mocks.signUploadParts.mockImplementation(async (_id, partNumbers) =>
      partNumbers.map((partNumber: number) => ({
        partNumber,
        size: partNumber === 3 ? 1024 * 1024 : 32 * 1024 * 1024,
        upload: {
          method: "PUT",
          url: `/upload/${partNumber}`,
          headers: {},
        },
      })),
    );
    let permitUpload = false;
    const activeSignals: AbortSignal[] = [];
    mocks.uploadSignedBlob.mockImplementation(
      async (upload, _content, progress, signal: AbortSignal) => {
        progress(50);
        activeSignals.push(signal);
        if (!permitUpload) {
          await new Promise<void>((_resolve, reject) =>
            signal.addEventListener(
              "abort",
              () => reject(new DOMException("aborted", "AbortError")),
              { once: true },
            ),
          );
        }
        progress(100);
        return `"etag-${upload.url.at(-1)}"`;
      },
    );
    mocks.acknowledgeUploadParts.mockImplementation(async (_id, parts) => {
      current = {
        ...current,
        completedParts: [
          ...current.completedParts,
          ...parts.map((part: { partNumber: number }) => ({
            partNumber: part.partNumber,
            size: part.partNumber === 3 ? 1024 * 1024 : 32 * 1024 * 1024,
            confirmedAt: "2026-08-11T00:00:00Z",
          })),
        ],
      };
      return current;
    });
    renderPage();
    fireEvent.change(await findFileInput(), {
      target: { files: [sizedFile("movie.bin", 65 * 1024 * 1024)] },
    });
    fireEvent.click(
      await screen.findByRole("button", { name: "Start 1 transfers" }),
    );
    await waitFor(() =>
      expect(mocks.uploadSignedBlob).toHaveBeenCalledTimes(2),
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: "Pause movie.bin immediately",
      }),
    );

    await screen.findByText("Paused");
    expect(activeSignals).toHaveLength(2);
    expect(activeSignals.every((signal) => signal.aborted)).toBe(true);
    expect(
      screen.getByRole("progressbar", {
        name: "Upload progress for movie.bin",
      }),
    ).toHaveAttribute("value", "0");
    expect(mocks.completeUploadSession).not.toHaveBeenCalled();
    const continueButton = screen.getByRole("button", {
      name: "Continue uploading movie.bin",
    });
    await waitFor(() => expect(continueButton).toBeEnabled());
    permitUpload = true;
    fireEvent.click(continueButton);

    await waitFor(() => expect(mocks.completeUploadSession).toHaveBeenCalled());
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [1]);
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [2]);
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [3]);
  });

  it("sandboxes PDF previews and obeys previewKind for forced downloads", async () => {
    mocks.listFiles.mockResolvedValue({
      items: [
        fileRecord("file-pdf", "guide.pdf", "pdf", "application/pdf"),
        fileRecord("file-download", "legacy.pdf", "none", "application/pdf"),
      ],
      page: 1,
      pageSize: 20,
      total: 2,
    });
    mocks.getFileURL.mockImplementation(async (id, purpose) => ({
      method: "GET",
      url: `https://files.test/${id}?purpose=${purpose}`,
      headers: {},
    }));
    const open = vi.fn();
    vi.stubGlobal("open", open);
    renderPage();

    fireEvent.click(
      await screen.findByRole("button", { name: "Preview guide.pdf" }),
    );

    const frame = await screen.findByTitle("Preview guide.pdf");
    expect(frame).toHaveAttribute("sandbox", "");
    expect(frame).toHaveAttribute(
      "src",
      "https://files.test/file-pdf?purpose=preview",
    );
    expect(mocks.getFileURL).toHaveBeenCalledWith("file-pdf", "preview");
    expect(open).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Close PDF preview" }));
    fireEvent.click(
      screen.getByRole("button", { name: "Download legacy.pdf" }),
    );

    await waitFor(() =>
      expect(mocks.getFileURL).toHaveBeenCalledWith(
        "file-download",
        "download",
      ),
    );
    expect(open).toHaveBeenCalledWith(
      "https://files.test/file-download?purpose=download",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("recovers a refreshed session after reselecting the matching file", async () => {
    mocks.getFileUploadPolicy.mockResolvedValue(resumablePolicy());
    let current = uploadSession("dataset.bin", 65 * 1024 * 1024, 3);
    current = {
      ...current,
      completedParts: [
        {
          partNumber: 1,
          size: 32 * 1024 * 1024,
          confirmedAt: "2026-08-11T00:00:00Z",
        },
      ],
    };
    mocks.listIncompleteUploadSessions.mockResolvedValue({
      items: [current],
      page: 1,
      pageSize: 20,
      total: 1,
    });
    mocks.resumeUploadSession.mockResolvedValue(current);
    mocks.signUploadParts.mockImplementation(async (_id, [partNumber]) => [
      signedPart(partNumber, partNumber === 3 ? 1024 * 1024 : 32 * 1024 * 1024),
    ]);
    mocks.acknowledgeUploadParts.mockImplementation(async (_id, parts) => ({
      ...current,
      completedParts: [
        ...current.completedParts,
        ...parts.map((part: { partNumber: number }) => ({
          partNumber: part.partNumber,
          size: part.partNumber === 3 ? 1024 * 1024 : 32 * 1024 * 1024,
          confirmedAt: "2026-08-11T00:00:00Z",
        })),
      ],
    }));
    renderPage();
    await screen.findByText("Incomplete transfers");
    const recoveryInput = document.querySelector(".recovery-list input");
    expect(recoveryInput).toBeInstanceOf(HTMLInputElement);

    fireEvent.change(recoveryInput as HTMLInputElement, {
      target: {
        files: [sizedFile("dataset.bin", 65 * 1024 * 1024)],
      },
    });

    await waitFor(() =>
      expect(mocks.resumeUploadSession).toHaveBeenCalledWith(
        current.id,
        "a".repeat(64),
      ),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Start 1 transfers" }),
    );
    await waitFor(() => expect(mocks.completeUploadSession).toHaveBeenCalled());
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [2]);
    expect(mocks.signUploadParts).toHaveBeenCalledWith(current.id, [3]);
  });
});

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
      <QueryClientProvider client={queryClient}>
        <FilesPage />
      </QueryClientProvider>
    </NextIntlClientProvider>,
  );
}

async function findFileInput() {
  await screen.findByText("Drop files into the staging area");
  const input = document.querySelector('input[name="files"]');
  if (!(input instanceof HTMLInputElement)) {
    throw new Error("File input was not rendered.");
  }
  return input;
}

function resumablePolicy() {
  return {
    maxUploadBytes: 1024 * 1024 * 1024,
    resumableUploadsEnabled: true,
    resumableAvailable: true,
    multipartThresholdBytes: 32 * 1024 * 1024,
    multipartPartSizeBytes: 32 * 1024 * 1024,
    sessionTtlSeconds: 86_400,
    maxBatchFiles: 20,
  };
}

function sizedFile(name: string, size: number) {
  const file = new File(["sample"], name, {
    type: "application/octet-stream",
    lastModified: 1_786_406_400_000,
  });
  Object.defineProperty(file, "size", { configurable: true, value: size });
  return file;
}

function uploadSession(name: string, size: number, partCount: number) {
  return {
    id: `session-${name}`,
    file: {
      id: `file-${name}`,
      originalName: name,
      contentType: "application/octet-stream",
      size,
    },
    status: "active",
    partSize: 32 * 1024 * 1024,
    partCount,
    completedParts: [] as Array<{
      partNumber: number;
      size: number;
      confirmedAt: string;
    }>,
    expiresAt: "2026-08-12T00:00:00Z",
    createdAt: "2026-08-11T00:00:00Z",
    updatedAt: "2026-08-11T00:00:00Z",
  };
}

function signedPart(partNumber: number, size = 32 * 1024 * 1024) {
  return {
    partNumber,
    size,
    upload: {
      method: "PUT",
      url: `/upload/${partNumber}`,
      headers: {},
    },
  };
}

function fileRecord(
  id: string,
  originalName: string,
  previewKind: "image" | "pdf" | "none",
  contentType: string,
) {
  return {
    id,
    originalName,
    previewKind,
    contentType,
    provider: "local",
    sha256: "a".repeat(64),
    size: 1024,
    status: "ready",
    visibility: "private",
    width: 0,
    height: 0,
    createdAt: "2026-08-11T00:00:00Z",
    updatedAt: "2026-08-11T00:00:00Z",
  };
}

function statusError(status: number) {
  return Object.assign(new Error(`status ${status}`), { status });
}
