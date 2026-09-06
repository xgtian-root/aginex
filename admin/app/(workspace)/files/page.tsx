"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  Download,
  Eye,
  FileArchive,
  FileCheck2,
  FileClock,
  FileUp,
  Pause,
  Play,
  RotateCcw,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { type DragEvent, useEffect, useId, useRef, useState } from "react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { hasGrant } from "@/lib/access";
import {
  acknowledgeUploadParts,
  completeUploadSession,
  confirmUpload,
  createUploadIntent,
  deleteFile as deleteFileRequest,
  type FileObject,
  getCurrentUser,
  getFileUploadPolicy,
  getFileURL,
  listFiles,
  listIncompleteUploadSessions,
  ObjectUploadError,
  resumeUploadSession,
  signUploadParts,
  type UploadSession,
  type UploadStrategy,
  uploadPreparedFile,
  uploadSignedBlob,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import {
  completedSessionBytes,
  createTransferLimiter,
  fileResumeFingerprint,
  isAbortError,
  missingPartNumbers,
  partByteRange,
  retryTransferOperation,
  type TransferLimiter,
} from "./transfer";
import "@/components/page-header.css";
import "@/components/management-console.css";
import "./files.css";

const MAX_BATCH_FILES = 20;
const MAX_CONCURRENT_UPLOADS = 4;
const MAX_PARTS_PER_FILE = 2;

type TransferStatus =
  | "queued"
  | "uploading"
  | "paused"
  | "verifying"
  | "success"
  | "error";

type TransferItem = {
  id: string;
  file: File;
  status: TransferStatus;
  progress: number;
  strategy: UploadStrategy;
  fingerprint?: string;
  session?: UploadSession;
  error?: string;
};

type TransferOutcome = "success" | "paused" | "error";
type DeleteTarget = Pick<FileObject, "id" | "originalName"> | null;
type PdfPreview = { name: string; url: string } | null;

export default function FilesPage() {
  const t = useTranslations("Files");
  const translate = useTranslations();
  const format = useFormatter();
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const pauseRequests = useRef(new Set<string>());
  const abortControllers = useRef(new Map<string, AbortController>());
  const acknowledgedSessions = useRef(new Map<string, UploadSession>());
  const runningRef = useRef(false);
  const deferredContinuations = useRef(new Map<string, TransferItem>());
  const [queue, setQueue] = useState<TransferItem[]>([]);
  const [running, setRunning] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget>(null);
  const [pdfPreview, setPdfPreview] = useState<PdfPreview>(null);
  const [recovering, setRecovering] = useState(new Set<string>());

  const me = useQuery({ queryKey: ["me"], queryFn: getCurrentUser });
  const canRead = hasGrant(me.data, "files:read", "own");
  const canCreate = hasGrant(me.data, "files:create", "own");
  const canDelete = hasGrant(me.data, "files:delete", "own");
  const files = useQuery({
    queryKey: ["files"],
    queryFn: listFiles,
    enabled: canRead,
  });
  const policy = useQuery({
    queryKey: ["file-upload-policy"],
    queryFn: getFileUploadPolicy,
    enabled: canCreate,
  });
  const resumableEnabled = Boolean(
    policy.data?.resumableUploadsEnabled && policy.data.resumableAvailable,
  );
  const incomplete = useQuery({
    queryKey: ["incomplete-upload-sessions"],
    queryFn: listIncompleteUploadSessions,
    enabled: canCreate && resumableEnabled,
  });
  const deleteFile = useMutation({
    mutationFn: deleteFileRequest,
    onSuccess: () => {
      setDeleteTarget(null);
      queryClient.invalidateQueries({ queryKey: ["files"] });
      toast.success(t("deletedToast"));
    },
    onError: (error) =>
      toast.error(localizeApiError(error, translate, t("deleteError"))),
  });

  const updateItem = (id: string, patch: Partial<TransferItem>) => {
    setQueue((current) =>
      current.map((item) => (item.id === id ? { ...item, ...patch } : item)),
    );
  };

  const pauseItem = (
    item: TransferItem,
    session = acknowledgedSessions.current.get(item.id) ?? item.session,
  ): TransferOutcome => {
    const progress = session
      ? Math.round((completedSessionBytes(session) / item.file.size) * 100)
      : 0;
    updateItem(item.id, { status: "paused", progress, session });
    return "paused";
  };

  const strategyFor = (file: File): UploadStrategy =>
    resumableEnabled &&
    policy.data &&
    file.size > policy.data.multipartThresholdBytes
      ? "resumable"
      : "single";

  const enqueue = (selection: FileList | File[]) => {
    if (running || !policy.data) return;
    const batchLimit = Math.min(
      MAX_BATCH_FILES,
      policy.data.maxBatchFiles || MAX_BATCH_FILES,
    );
    const available = Math.max(0, batchLimit - queue.length);
    const candidates = Array.from(selection);
    const accepted = candidates.slice(0, available);
    if (accepted.length < candidates.length) {
      toast.error(t("upload.limitError", { count: batchLimit }));
    }
    const additions = accepted.map<TransferItem>((file) => {
      const validationError =
        file.size === 0
          ? t("upload.emptyFile")
          : file.size > policy.data.maxUploadBytes
            ? t("upload.tooLarge", {
                limit: formatBytes(policy.data.maxUploadBytes, format, t),
              })
            : undefined;
      return {
        id: crypto.randomUUID(),
        file,
        strategy: strategyFor(file),
        status: validationError ? "error" : "queued",
        progress: 0,
        error: validationError,
      };
    });
    setQueue((current) => [...current, ...additions]);
  };

  const uploadResumable = async (
    item: TransferItem,
    initialSession: UploadSession,
    limiter: TransferLimiter,
    signal: AbortSignal,
  ): Promise<TransferOutcome> => {
    let session = initialSession;
    acknowledgedSessions.current.set(item.id, session);
    let remaining = missingPartNumbers(session);
    while (remaining.length > 0) {
      if (pauseRequests.current.has(item.id)) return pauseItem(item, session);
      const wave = remaining.slice(0, MAX_PARTS_PER_FILE);
      const livePartBytes = new Map<number, number>();
      const acknowledged = await Promise.all(
        wave.map((partNumber) =>
          retryTransferOperation(
            async () => {
              livePartBytes.set(partNumber, 0);
              const [part] = await signUploadParts(session.id, [partNumber]);
              if (!part || part.partNumber !== partNumber) {
                throw new Error("The API returned no signed upload part.");
              }
              const { start, end } = partByteRange(
                item.file.size,
                session.partSize,
                part.partNumber,
              );
              const content = item.file.slice(start, end);
              const etag = await limiter(() =>
                uploadSignedBlob(
                  part.upload,
                  content,
                  (percentage) => {
                    if (signal.aborted) return;
                    livePartBytes.set(
                      part.partNumber,
                      Math.round((content.size * percentage) / 100),
                    );
                    const uploaded =
                      completedSessionBytes(session) +
                      Array.from(livePartBytes.values()).reduce(
                        (total, bytes) => total + bytes,
                        0,
                      );
                    updateItem(item.id, {
                      progress: Math.min(
                        99,
                        Math.round((uploaded / item.file.size) * 100),
                      ),
                    });
                  },
                  signal,
                ),
              );
              if (!etag) throw new Error("The part upload returned no ETag.");
              return { partNumber: part.partNumber, etag };
            },
            { signal },
          ),
        ),
      );
      session = await retryTransferOperation(
        () => acknowledgeUploadParts(session.id, acknowledged),
        { signal },
      );
      acknowledgedSessions.current.set(item.id, session);
      updateItem(item.id, { session });
      remaining = missingPartNumbers(session);
      if (pauseRequests.current.has(item.id)) return pauseItem(item, session);
    }

    pauseRequests.current.delete(item.id);
    updateItem(item.id, { status: "verifying", progress: 100, session });
    await retryTransferOperation(() => completeUploadSession(session.id), {
      signal,
    });
    updateItem(item.id, { status: "success", progress: 100, session });
    return "success";
  };

  const transfer = async (
    item: TransferItem,
    limiter: TransferLimiter,
  ): Promise<TransferOutcome> => {
    const controller = new AbortController();
    abortControllers.current.set(item.id, controller);
    if (item.session) acknowledgedSessions.current.set(item.id, item.session);
    updateItem(item.id, {
      status: "uploading",
      error: undefined,
      progress: item.session
        ? Math.round(
            (completedSessionBytes(item.session) / item.file.size) * 100,
          )
        : 0,
    });
    try {
      let fingerprint = item.fingerprint;
      let session = item.session;
      let strategy = item.strategy;
      const intent = {
        filename: item.file.name,
        contentType: item.file.type || "application/octet-stream",
        size: item.file.size,
        visibility: "private" as const,
        strategy,
      };
      if (!session && strategy === "single") {
        await retryTransferOperation(
          async () => {
            updateItem(item.id, { status: "uploading", progress: 0 });
            // A failed direct transfer is never resumed against an uncertain
            // object. Every retry prepares a new intent and destination.
            const prepared = await createUploadIntent(
              intent,
              crypto.randomUUID(),
            );
            await limiter(() =>
              uploadPreparedFile(
                prepared,
                item.file,
                (progress) => updateItem(item.id, { progress }),
                controller.signal,
              ),
            );
            updateItem(item.id, { status: "verifying", progress: 100 });
            await confirmUpload(prepared.file.id);
          },
          { signal: controller.signal },
        );
        updateItem(item.id, { status: "success", progress: 100 });
        return "success";
      }
      if (!session) {
        fingerprint = await fileResumeFingerprint(item.file);
        const requestKey = crypto.randomUUID();
        const prepared = await retryTransferOperation(
          () =>
            createUploadIntent(
              { ...intent, resumeFingerprint: fingerprint },
              requestKey,
            ),
          { signal: controller.signal },
        );
        strategy = prepared.strategy;
        session = prepared.session;
        if (session) acknowledgedSessions.current.set(item.id, session);
        updateItem(item.id, { strategy, fingerprint, session });
      }
      if (!session) throw new Error("The API returned no upload session.");
      return await uploadResumable(item, session, limiter, controller.signal);
    } catch (error) {
      if (isAbortError(error) && pauseRequests.current.has(item.id)) {
        return pauseItem(item);
      }
      const fallback =
        error instanceof ObjectUploadError
          ? error.status === 0
            ? t("cloudUploadConnectionError")
            : t("objectUploadError", { status: error.status })
          : t("uploadError");
      updateItem(item.id, {
        status: "error",
        error: localizeApiError(error, translate, fallback),
      });
      return "error";
    } finally {
      if (abortControllers.current.get(item.id) === controller) {
        abortControllers.current.delete(item.id);
      }
    }
  };

  const runTransfers = async (pending: TransferItem[]) => {
    if (runningRef.current || pending.length === 0) return;
    runningRef.current = true;
    setRunning(true);
    const limiter = createTransferLimiter(MAX_CONCURRENT_UPLOADS);
    let cursor = 0;
    const outcomes: TransferOutcome[] = [];
    const worker = async () => {
      while (cursor < pending.length) {
        const item = pending[cursor];
        cursor += 1;
        outcomes.push(await transfer(item, limiter));
      }
    };
    try {
      await Promise.all(
        Array.from(
          { length: Math.min(MAX_CONCURRENT_UPLOADS, pending.length) },
          worker,
        ),
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["files"] }),
        queryClient.invalidateQueries({
          queryKey: ["incomplete-upload-sessions"],
        }),
      ]);
      const succeeded = outcomes.filter((value) => value === "success").length;
      const failed = outcomes.filter((value) => value === "error").length;
      const paused = outcomes.filter((value) => value === "paused").length;
      if (failed > 0) {
        toast.error(t("upload.batchResult", { succeeded, failed, paused }));
      } else if (paused > 0) {
        toast.success(t("upload.batchPaused", { succeeded, paused }));
      } else {
        toast.success(t("upload.batchSuccess", { count: succeeded }));
      }
    } finally {
      runningRef.current = false;
      setRunning(false);
      const deferred = Array.from(deferredContinuations.current.values());
      deferredContinuations.current.clear();
      if (deferred.length > 0) void runTransfers(deferred);
    }
  };

  const startBatch = () =>
    runTransfers(queue.filter((item) => item.status === "queued"));

  const pauseTransfer = (item: TransferItem) => {
    pauseRequests.current.add(item.id);
    pauseItem(item);
    abortControllers.current.get(item.id)?.abort();
  };

  const continueTransfer = (item: TransferItem) => {
    pauseRequests.current.delete(item.id);
    const resumed = { ...item, status: "queued" as const, error: undefined };
    updateItem(item.id, resumed);
    if (runningRef.current) {
      deferredContinuations.current.set(item.id, resumed);
    } else {
      void runTransfers([resumed]);
    }
  };

  const recoverSession = async (session: UploadSession, file: File) => {
    setRecovering((current) => new Set(current).add(session.id));
    try {
      if (
        file.name !== session.file.originalName ||
        file.size !== session.file.size
      ) {
        toast.error(t("recovery.fileMismatch"));
        return;
      }
      const fingerprint = await fileResumeFingerprint(file);
      const resumed = await retryTransferOperation(() =>
        resumeUploadSession(session.id, fingerprint),
      );
      setQueue((current) => [
        ...current,
        {
          id: crypto.randomUUID(),
          file,
          status: "queued",
          progress: Math.round(
            (completedSessionBytes(resumed) / file.size) * 100,
          ),
          strategy: "resumable",
          fingerprint,
          session: resumed,
        },
      ]);
      toast.success(t("recovery.matchedToast", { name: file.name }));
    } catch (error) {
      toast.error(localizeApiError(error, translate, t("recovery.matchError")));
    } finally {
      setRecovering((current) => {
        const next = new Set(current);
        next.delete(session.id);
        return next;
      });
    }
  };

  async function openFile(file: FileObject) {
    const purpose = file.previewKind === "none" ? "download" : "preview";
    try {
      const signed = await getFileURL(file.id, purpose);
      if (file.previewKind === "pdf") {
        setPdfPreview({ name: file.originalName, url: signed.url });
      } else {
        window.open(signed.url, "_blank", "noopener,noreferrer");
      }
    } catch (error) {
      toast.error(localizeApiError(error, translate, t("temporaryUrlError")));
    }
  }

  if (me.isPending) return <PageState text={t("states.loading")} />;
  if (me.isError || !me.data)
    return <PageState error text={t("states.unavailable")} />;
  if (!canRead) return <PageState error text={t("states.forbidden")} />;

  const queued = queue.filter((item) => item.status === "queued").length;
  const succeeded = queue.filter((item) => item.status === "success").length;
  const failed = queue.filter((item) => item.status === "error").length;
  const totalBytes = queue.reduce((total, item) => total + item.file.size, 0);
  const transferredBytes = queue.reduce(
    (total, item) => total + item.file.size * (item.progress / 100),
    0,
  );
  const batchProgress = totalBytes
    ? Math.round((transferredBytes / totalBytes) * 100)
    : 0;
  const queuedSessionIDs = new Set(
    queue.flatMap((item) => (item.session ? [item.session.id] : [])),
  );
  const recoverable =
    incomplete.data?.items.filter(
      (session) => !queuedSessionIDs.has(session.id),
    ) ?? [];

  return (
    <div className="files-console">
      <PageHeader
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />

      {canCreate && (
        <section
          className="transfer-workbench panel"
          aria-labelledby="transfer-title"
        >
          <div className="transfer-heading">
            <div>
              <p className="transfer-kicker">{t("upload.kicker")}</p>
              <h2 id="transfer-title">{t("upload.title")}</h2>
              <p>{t("upload.description")}</p>
            </div>
            <fieldset className="transfer-spec">
              <legend className="sr-only">{t("upload.specAriaLabel")}</legend>
              <span>{t("upload.privateBadge")}</span>
              <span>
                {t("upload.concurrent", { count: MAX_CONCURRENT_UPLOADS })}
              </span>
              <span>
                {t(
                  resumableEnabled
                    ? "upload.resumableStrategy"
                    : "upload.directStrategy",
                )}
              </span>
            </fieldset>
          </div>

          {policy.isPending ? (
            <div className="transfer-policy-state" role="status">
              {t("upload.policyLoading")}
            </div>
          ) : policy.isError || !policy.data ? (
            <div className="transfer-policy-state danger" role="alert">
              <span>{t("upload.policyError")}</span>
              <button
                className="button secondary"
                onClick={() => policy.refetch()}
                type="button"
              >
                {t("actions.retry")}
              </button>
            </div>
          ) : (
            <>
              <button
                className={`transfer-dropzone ${dragActive ? "active" : ""}`}
                disabled={running}
                onClick={() => !running && inputRef.current?.click()}
                onDragEnter={(event) => handleDrag(event, setDragActive, true)}
                onDragLeave={(event) => handleDrag(event, setDragActive, false)}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  setDragActive(false);
                  enqueue(event.dataTransfer.files);
                }}
                type="button"
              >
                <span className="transfer-dropzone__icon">
                  <UploadCloud aria-hidden size={26} />
                </span>
                <span className="transfer-dropzone__copy">
                  <strong>{t("upload.dropTitle")}</strong>
                  <span>
                    {t("upload.requirements", {
                      count: Math.min(
                        MAX_BATCH_FILES,
                        policy.data.maxBatchFiles || MAX_BATCH_FILES,
                      ),
                      size: formatBytes(policy.data.maxUploadBytes, format, t),
                    })}
                  </span>
                </span>
                <span className="button secondary transfer-browse" aria-hidden>
                  <FileUp size={17} />
                  {t("upload.choose")}
                </span>
              </button>
              <input
                className="transfer-file-input"
                disabled={running}
                multiple
                name="files"
                onChange={(event) => {
                  if (event.target.files) enqueue(event.target.files);
                  event.target.value = "";
                }}
                ref={inputRef}
                tabIndex={-1}
                type="file"
              />

              <div className="transfer-ledger" aria-live="polite">
                <div className="transfer-ledger__summary">
                  <div>
                    <strong>{t("queue.title")}</strong>
                    <span>
                      {t("queue.summary", {
                        total: queue.length,
                        queued,
                        succeeded,
                        failed,
                      })}
                    </span>
                  </div>
                  <div className="transfer-ledger__actions">
                    {failed > 0 && !running && (
                      <button
                        className="button secondary"
                        onClick={() =>
                          setQueue((current) =>
                            current.map((item) =>
                              item.status === "error" &&
                              item.file.size > 0 &&
                              item.file.size <= policy.data.maxUploadBytes
                                ? {
                                    ...item,
                                    status: "queued",
                                    error: undefined,
                                  }
                                : item,
                            ),
                          )
                        }
                        type="button"
                      >
                        <RotateCcw size={16} />
                        {t("queue.retryFailed")}
                      </button>
                    )}
                    {queue.length > 0 && !running && (
                      <button
                        className="button secondary"
                        onClick={() => setQueue([])}
                        type="button"
                      >
                        {t("queue.clear")}
                      </button>
                    )}
                    <button
                      className="button"
                      disabled={running || queued === 0}
                      onClick={startBatch}
                      type="button"
                    >
                      <FileUp size={17} />
                      {running
                        ? t("queue.running")
                        : t("queue.start", { count: queued })}
                    </button>
                  </div>
                </div>

                {queue.length > 0 && (
                  <div className="batch-progress">
                    <span>{t("queue.batchProgress")}</span>
                    <progress max={100} value={batchProgress}>
                      {batchProgress}%
                    </progress>
                    <strong>{batchProgress}%</strong>
                  </div>
                )}

                {queue.length === 0 ? (
                  <div className="transfer-empty">
                    <FileArchive aria-hidden size={24} />
                    <p>{t("queue.empty")}</p>
                  </div>
                ) : (
                  <ul
                    className="transfer-list"
                    aria-label={t("queue.ariaLabel")}
                  >
                    {queue.map((item) => (
                      <TransferRow
                        busy={running}
                        format={format}
                        item={item}
                        key={item.id}
                        onContinue={() => continueTransfer(item)}
                        onPause={() => pauseTransfer(item)}
                        onRemove={() =>
                          setQueue((current) =>
                            current.filter(
                              (candidate) => candidate.id !== item.id,
                            ),
                          )
                        }
                        resumableControls={resumableEnabled}
                        t={t}
                      />
                    ))}
                  </ul>
                )}
              </div>
            </>
          )}
        </section>
      )}

      {resumableEnabled && (incomplete.isError || recoverable.length > 0) && (
        <section
          className="recovery-panel panel"
          aria-labelledby="recovery-title"
        >
          <div className="inventory-heading">
            <div>
              <p className="transfer-kicker">{t("recovery.kicker")}</p>
              <h2 id="recovery-title">{t("recovery.title")}</h2>
            </div>
            <span>{t("recovery.count", { count: recoverable.length })}</span>
          </div>
          {incomplete.isError ? (
            <PageState
              action={
                <button
                  className="button secondary"
                  onClick={() => incomplete.refetch()}
                  type="button"
                >
                  {t("actions.retry")}
                </button>
              }
              error
              text={t("recovery.loadError")}
            />
          ) : (
            <ul className="recovery-list">
              {recoverable.map((session) => (
                <li key={session.id}>
                  <span className="transfer-row__icon">
                    <RotateCcw aria-hidden size={18} />
                  </span>
                  <div>
                    <strong>{session.file.originalName}</strong>
                    <span>
                      {t("recovery.progress", {
                        complete: session.completedParts.length,
                        total: session.partCount,
                        expires: format.dateTime(new Date(session.expiresAt), {
                          dateStyle: "medium",
                          timeStyle: "short",
                        }),
                      })}
                    </span>
                  </div>
                  <label className="button secondary">
                    <FileUp size={16} />
                    {recovering.has(session.id)
                      ? t("recovery.matching")
                      : t("recovery.choose")}
                    <input
                      disabled={recovering.has(session.id) || running}
                      onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) void recoverSession(session, file);
                        event.target.value = "";
                      }}
                      type="file"
                    />
                  </label>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      <FileInventory
        canDelete={canDelete}
        deleteTarget={setDeleteTarget}
        files={files}
        format={format}
        onOpen={openFile}
        t={t}
      />

      <ConfirmDialog
        busy={deleteFile.isPending}
        cancelLabel={t("actions.cancel")}
        confirmLabel={t("deleteConfirm.action")}
        description={
          deleteTarget
            ? t("deleteConfirm.description", {
                name: deleteTarget.originalName,
              })
            : ""
        }
        destructive
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteFile.mutate(deleteTarget.id)}
        open={deleteTarget !== null}
        title={t("deleteConfirm.title")}
      />
      <PdfPreviewDialog
        closeLabel={t("preview.close")}
        file={pdfPreview}
        onClose={() => setPdfPreview(null)}
        title={pdfPreview ? t("preview.title", { name: pdfPreview.name }) : ""}
      />
    </div>
  );
}

function TransferRow({
  item,
  busy,
  onRemove,
  onPause,
  onContinue,
  resumableControls,
  format,
  t,
}: {
  item: TransferItem;
  busy: boolean;
  onRemove: () => void;
  onPause: () => void;
  onContinue: () => void;
  resumableControls: boolean;
  format: ReturnType<typeof useFormatter>;
  t: ReturnType<typeof useTranslations<"Files">>;
}) {
  const active = item.status === "uploading" || item.status === "verifying";
  const icon =
    item.status === "success" ? (
      <CheckCircle2 aria-hidden size={19} />
    ) : active || item.status === "paused" ? (
      <FileClock aria-hidden size={19} />
    ) : (
      <FileArchive aria-hidden size={19} />
    );
  return (
    <li className={`transfer-row ${item.status}`}>
      <span className="transfer-row__icon">{icon}</span>
      <div className="transfer-row__identity">
        <strong title={item.file.name}>{item.file.name}</strong>
        <span>
          {formatBytes(item.file.size, format, t)} ·{" "}
          {t(`queue.strategy.${item.strategy}`)}
        </span>
      </div>
      <div className="transfer-row__meter">
        <div>
          <span>{t(`queue.status.${item.status}`)}</span>
          <strong>{item.progress}%</strong>
        </div>
        <progress
          aria-label={t("queue.fileProgress", { name: item.file.name })}
          max={100}
          value={item.progress}
        />
        {item.error && <p role="alert">{item.error}</p>}
      </div>
      <div className="transfer-row__actions">
        {resumableControls &&
          item.strategy === "resumable" &&
          item.status === "uploading" && (
            <button
              aria-label={t("queue.pauseAriaLabel", { name: item.file.name })}
              className="icon-button"
              onClick={onPause}
              type="button"
            >
              <Pause aria-hidden size={16} />
            </button>
          )}
        {resumableControls &&
          item.strategy === "resumable" &&
          item.status === "paused" && (
            <button
              aria-label={t("queue.continueAriaLabel", {
                name: item.file.name,
              })}
              className="icon-button"
              disabled={busy}
              onClick={onContinue}
              type="button"
            >
              <Play aria-hidden size={16} />
            </button>
          )}
        {!active && item.status !== "paused" && (
          <button
            aria-label={t("queue.removeAriaLabel", { name: item.file.name })}
            className="icon-button"
            onClick={onRemove}
            type="button"
          >
            <X aria-hidden size={16} />
          </button>
        )}
      </div>
    </li>
  );
}

function FileInventory({
  files,
  canDelete,
  deleteTarget,
  onOpen,
  format,
  t,
}: {
  files: ReturnType<typeof useQuery<Awaited<ReturnType<typeof listFiles>>>>;
  canDelete: boolean;
  deleteTarget: (file: FileObject) => void;
  onOpen: (file: FileObject) => void;
  format: ReturnType<typeof useFormatter>;
  t: ReturnType<typeof useTranslations<"Files">>;
}) {
  return (
    <section className="file-inventory panel" aria-labelledby="inventory-title">
      <div className="inventory-heading">
        <div>
          <p className="transfer-kicker">{t("inventory.kicker")}</p>
          <h2 id="inventory-title">{t("inventory.title")}</h2>
        </div>
        <span>
          {files.data ? t("inventory.count", { count: files.data.total }) : "—"}
        </span>
      </div>
      {files.isPending ? (
        <PageState text={t("states.loadingFiles")} />
      ) : files.isError ? (
        <PageState
          action={
            <button
              className="button secondary"
              onClick={() => files.refetch()}
              type="button"
            >
              {t("actions.retry")}
            </button>
          }
          error
          text={t("states.filesUnavailable")}
        />
      ) : files.data.items.length === 0 ? (
        <div className="empty-state">
          <h2>{t("emptyTitle")}</h2>
          <p>{t("emptyDescription")}</p>
        </div>
      ) : (
        <div className="table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{t("columns.file")}</th>
                <th>{t("columns.provider")}</th>
                <th>{t("columns.size")}</th>
                <th>{t("columns.visibility")}</th>
                <th>{t("columns.status")}</th>
                <th>
                  <span className="sr-only">{t("columns.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {files.data.items.map((file) => {
                const preview = file.previewKind !== "none";
                return (
                  <tr key={file.id}>
                    <td data-label={t("columns.file")}>
                      <span className="file-record-name">
                        {preview ? (
                          <FileCheck2 aria-hidden size={18} />
                        ) : (
                          <FileArchive aria-hidden size={18} />
                        )}
                        <span>
                          <strong>{file.originalName}</strong>
                          <small>
                            {file.contentType || t("values.unknownType")}
                          </small>
                        </span>
                      </span>
                    </td>
                    <td data-label={t("columns.provider")}>
                      {file.storageProfileName ||
                        file.storageProvider ||
                        t(`providers.${file.provider}`)}
                    </td>
                    <td data-label={t("columns.size")}>
                      {formatBytes(file.size, format, t)}
                    </td>
                    <td data-label={t("columns.visibility")}>
                      {t(`visibility.${file.visibility}`)}
                    </td>
                    <td data-label={t("columns.status")}>
                      <span className="status">
                        {t(`statuses.${file.status}`)}
                      </span>
                    </td>
                    <td data-label={t("columns.actions")}>
                      <div className="file-actions">
                        <button
                          aria-label={t(
                            preview ? "previewAriaLabel" : "downloadAriaLabel",
                            { name: file.originalName },
                          )}
                          className="icon-button"
                          disabled={file.status !== "ready"}
                          onClick={() => onOpen(file)}
                          type="button"
                        >
                          {preview ? (
                            <Eye aria-hidden size={16} />
                          ) : (
                            <Download aria-hidden size={16} />
                          )}
                        </button>
                        {canDelete && (
                          <button
                            aria-label={t("deleteAriaLabel", {
                              name: file.originalName,
                            })}
                            className="icon-button delete-button"
                            onClick={() => deleteTarget(file)}
                            type="button"
                          >
                            <Trash2 aria-hidden size={16} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function PdfPreviewDialog({
  file,
  title,
  closeLabel,
  onClose,
}: {
  file: PdfPreview;
  title: string;
  closeLabel: string;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const titleId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (file && !dialog.open) {
      returnFocusRef.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
      closeRef.current?.focus();
      return;
    }
    if (!file && dialog.open) {
      if (typeof dialog.close === "function") dialog.close();
      else dialog.removeAttribute("open");
      returnFocusRef.current?.focus();
      returnFocusRef.current = null;
    }
  }, [file]);

  return (
    <dialog
      aria-labelledby={titleId}
      className="pdf-preview-dialog"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      ref={dialogRef}
    >
      <header>
        <h2 id={titleId}>{title}</h2>
        <button
          aria-label={closeLabel}
          className="icon-button"
          onClick={onClose}
          ref={closeRef}
          type="button"
        >
          <X aria-hidden size={18} />
        </button>
      </header>
      {file && (
        <iframe
          referrerPolicy="no-referrer"
          sandbox=""
          src={file.url}
          title={title}
        />
      )}
    </dialog>
  );
}

function handleDrag(
  event: DragEvent<HTMLElement>,
  setActive: (active: boolean) => void,
  active: boolean,
) {
  event.preventDefault();
  if (event.currentTarget.contains(event.relatedTarget as Node)) return;
  setActive(active);
}

function formatBytes(
  bytes: number,
  format: ReturnType<typeof useFormatter>,
  t: ReturnType<typeof useTranslations<"Files">>,
) {
  if (bytes < 1024) return `${format.number(bytes)} ${t("units.byte")}`;
  if (bytes < 1024 * 1024)
    return `${format.number(bytes / 1024, { maximumFractionDigits: 1 })} ${t("units.kilobyte")}`;
  if (bytes < 1024 * 1024 * 1024)
    return `${format.number(bytes / (1024 * 1024), { maximumFractionDigits: 1 })} ${t("units.megabyte")}`;
  return `${format.number(bytes / (1024 * 1024 * 1024), { maximumFractionDigits: 1 })} ${t("units.gigabyte")}`;
}

function PageState({
  text,
  error = false,
  action,
}: {
  text: string;
  error?: boolean;
  action?: React.ReactNode;
}) {
  return (
    <div
      className={error ? "management-error" : "empty-state"}
      role={error ? "alert" : "status"}
    >
      <p>{text}</p>
      {action}
    </div>
  );
}
