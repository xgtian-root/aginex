"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, ImageUp, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import {
  ApiError,
  confirmUpload,
  createUploadIntent,
  csrfHeaders,
  deleteFile as deleteFileRequest,
  getFileURL,
  listFiles,
} from "@/lib/api";
import "@/components/page-header.css";
import "../products/products.css";
import "./files.css";

export default function FilesPage() {
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
      toast.success("File deleted.");
    },
    onError: (error) =>
      toast.error(
        error instanceof ApiError
          ? error.problem.detail
          : "The file could not be deleted.",
      ),
  });

  async function upload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = event.currentTarget.elements.namedItem(
      "image",
    ) as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) {
      toast.error("Choose a JPEG, PNG, or WebP image.");
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
        throw new Error(`Object upload returned ${response.status}.`);
      }
      await confirmUpload(prepared.file.id);
      input.value = "";
      queryClient.invalidateQueries({ queryKey: ["files"] });
      toast.success("Image uploaded and verified.");
    } catch (error) {
      toast.error(
        error instanceof ApiError
          ? error.problem.detail
          : "The image could not be uploaded. Check its type and size, then try again.",
      );
    } finally {
      setUploading(false);
    }
  }

  async function openFile(id: string) {
    try {
      const signed = await getFileURL(id);
      window.open(signed.url, "_blank", "noopener,noreferrer");
    } catch {
      toast.error("A temporary file URL could not be created.");
    }
  }

  return (
    <>
      <PageHeader
        description="Verified image objects stored locally, in S3-compatible services, or Alibaba Cloud OSS through one provider contract."
        eyebrow="Object storage"
        title="Files"
      />
      <form className="upload-strip panel" onSubmit={upload}>
        <div>
          <strong>Upload a private image</strong>
          <span>JPEG, PNG, or WebP · up to 10 MB</span>
        </div>
        <label className="file-picker">
          <ImageUp aria-hidden size={18} />
          <span>Choose image</span>
          <input
            accept="image/jpeg,image/png,image/webp"
            name="image"
            required
            type="file"
          />
        </label>
        <button className="button" disabled={uploading} type="submit">
          {uploading ? "Uploading and verifying…" : "Upload image"}
        </button>
      </form>
      <section className="panel">
        {files.data?.items.length === 0 ? (
          <div className="empty-state">
            <h2>No files yet</h2>
            <p>
              Upload an image to exercise the provider-neutral storage workflow.
            </p>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>File</th>
                  <th>Provider</th>
                  <th>Size</th>
                  <th>Visibility</th>
                  <th>Status</th>
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {files.data?.items.map((file) => (
                  <tr key={file.id}>
                    <td data-label="File">
                      <strong>{file.originalName}</strong>
                    </td>
                    <td data-label="Provider">{file.provider}</td>
                    <td data-label="Size">{formatBytes(file.size)}</td>
                    <td data-label="Visibility">{file.visibility}</td>
                    <td data-label="Status">
                      <span className="status">{file.status}</span>
                    </td>
                    <td data-label="Actions">
                      <div className="file-actions">
                        <button
                          aria-label={`Open ${file.originalName}`}
                          className="icon-button"
                          disabled={file.status !== "ready"}
                          onClick={() => openFile(file.id)}
                          type="button"
                        >
                          <ExternalLink aria-hidden size={16} />
                        </button>
                        <button
                          aria-label={`Delete ${file.originalName}`}
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

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
