"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, ImageUp, Trash2 } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import {
  confirmUpload,
  createUploadIntent,
  csrfHeaders,
  deleteFile as deleteFileRequest,
  getFileURL,
  listFiles,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import "@/components/page-header.css";
import "../products/products.css";
import "./files.css";

export default function FilesPage() {
  const t = useTranslations("Files");
  const translate = useTranslations();
  const format = useFormatter();
  const queryClient = useQueryClient();
  const [uploading, setUploading] = useState(false);
  const files = useQuery({
    queryKey: ["files"],
    queryFn: listFiles,
  });
  const deleteFile = useMutation({
    mutationFn: deleteFileRequest,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["files"] });
      toast.success(t("deletedToast"));
    },
    onError: (error) =>
      toast.error(localizeApiError(error, translate, t("deleteError"))),
  });

  async function upload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = event.currentTarget.elements.namedItem(
      "image",
    ) as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) {
      toast.error(t("chooseImageError"));
      return;
    }
    setUploading(true);
    try {
      const prepared = await createUploadIntent({
        filename: file.name,
        contentType: file.type as "image/jpeg" | "image/png" | "image/webp",
        size: file.size,
        visibility: "private",
      });
      const localUpload = prepared.upload.url.includes(
        "/api/v1/files/local-upload/",
      );
      const uploadHeaders = new Headers(prepared.upload.headers);
      if (localUpload) {
        for (const [name, value] of Object.entries(await csrfHeaders())) {
          uploadHeaders.set(name, value);
        }
      }
      const response = await fetch(prepared.upload.url, {
        method: prepared.upload.method,
        headers: uploadHeaders,
        body: file,
        credentials: localUpload ? "include" : "omit",
      });
      if (!response.ok) {
        throw new ObjectUploadError(response.status);
      }
      await confirmUpload(prepared.file.id);
      input.value = "";
      queryClient.invalidateQueries({ queryKey: ["files"] });
      toast.success(t("uploadedToast"));
    } catch (error) {
      const fallback =
        error instanceof ObjectUploadError
          ? t("objectUploadError", { status: error.status })
          : t("uploadError");
      toast.error(localizeApiError(error, translate, fallback));
    } finally {
      setUploading(false);
    }
  }

  async function openFile(id: string) {
    try {
      const signed = await getFileURL(id);
      window.open(signed.url, "_blank", "noopener,noreferrer");
    } catch (error) {
      toast.error(localizeApiError(error, translate, t("temporaryUrlError")));
    }
  }

  function formatBytes(bytes: number) {
    if (bytes < 1024) {
      return `${format.number(bytes)} ${t("units.byte")}`;
    }
    if (bytes < 1024 * 1024) {
      return `${format.number(bytes / 1024, {
        minimumFractionDigits: 1,
        maximumFractionDigits: 1,
      })} ${t("units.kilobyte")}`;
    }
    return `${format.number(bytes / (1024 * 1024), {
      minimumFractionDigits: 1,
      maximumFractionDigits: 1,
    })} ${t("units.megabyte")}`;
  }

  return (
    <>
      <PageHeader
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />
      <form className="upload-strip panel" onSubmit={upload}>
        <div>
          <strong>{t("upload.title")}</strong>
          <span>{t("upload.requirements")}</span>
        </div>
        <label className="file-picker">
          <ImageUp aria-hidden size={18} />
          <span>{t("upload.choose")}</span>
          <input
            accept="image/jpeg,image/png,image/webp"
            name="image"
            required
            type="file"
          />
        </label>
        <button className="button" disabled={uploading} type="submit">
          {uploading ? t("upload.pending") : t("upload.submit")}
        </button>
      </form>
      <section className="panel">
        {files.data?.items.length === 0 ? (
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
                {files.data?.items.map((file) => (
                  <tr key={file.id}>
                    <td data-label={t("columns.file")}>
                      <strong>{file.originalName}</strong>
                    </td>
                    <td data-label={t("columns.provider")}>
                      {t(`providers.${file.provider}`)}
                    </td>
                    <td data-label={t("columns.size")}>
                      {formatBytes(file.size)}
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
                          aria-label={t("openAriaLabel", {
                            name: file.originalName,
                          })}
                          className="icon-button"
                          disabled={file.status !== "ready"}
                          onClick={() => openFile(file.id)}
                          type="button"
                        >
                          <ExternalLink aria-hidden size={16} />
                        </button>
                        <button
                          aria-label={t("deleteAriaLabel", {
                            name: file.originalName,
                          })}
                          className="icon-button delete-button"
                          onClick={() => deleteFile.mutate(file.id)}
                          type="button"
                        >
                          <Trash2 aria-hidden size={16} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

class ObjectUploadError extends Error {
  constructor(readonly status: number) {
    super();
    this.name = "ObjectUploadError";
  }
}
