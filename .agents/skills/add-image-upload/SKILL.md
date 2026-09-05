---
name: add-image-upload
description: Add safe Aginex image, attachment, and general file uploads through the Storage abstraction with Local, S3-compatible, and Alibaba Cloud OSS providers, including direct or resumable transfer, verification, metadata, signed access, permissions, and tests. Use for files, images, attachments, object storage, presigned uploads, multipart upload, S3, MinIO, R2, or OSS.
---

# Add File Upload

## Workflow

1. Use the shared `Storage` interface; business modules must not import cloud SDKs.
2. Create an upload intent with an opaque object key and short expiration.
3. Enforce the runtime size policy and treat browser MIME, extensions, and filenames only as hints. Store unknown or active content as downloads; never inline SVG or HTML.
4. Let the browser upload directly for cloud providers, then confirm through the API.
5. On confirmation, Stat/Head the object and compare size, MIME, bucket/key, and checksum when available before marking metadata ready.
6. Use short-lived signed URLs for private reads. Inline only verified safe preview types and force every other type to attachment. Represent delete failures as retryable state.
7. Protect intent, confirm, read, and delete operations with explicit permissions and audit writes.
8. When resumable upload is required, keep multipart SDK types behind an optional provider-neutral interface, persist provider ETags server-side, make completion/abort idempotent, and clean abandoned sessions.
9. Run Local and provider contract tests; use MinIO for S3-compatible CI.

## Constraints

- Never trust filename, browser MIME, bucket, key, or claimed size without verification.
- Never make credentials or private bucket URLs visible to the browser.
- Aginex is pre-release. Unless compatibility is explicitly requested, update the unpublished API, configuration, migration, generated-client, and UI baseline directly instead of adding legacy branches or version shims.
- Before an explicit release boundary, installation configuration always reads and writes the one strict version `1`, even when unpublished upload-policy fields change. Any other version and unknown fields fail closed. Do not add `current`/`legacy` migration families, adoption branches, old-client shims, or configuration version readers; keep one files migration family per database dialect. Retire stale local drafts only through the explicit recoverable `aginex dev reinitialize` workflow.
- Keep signed URLs, provider upload IDs, ETags, and file bytes out of logs, audit metadata, idempotency persistence, and browser durable storage.

## Completion Gate

Local, S3-compatible, and OSS adapters satisfy the same contract, active content cannot execute inline, metadata reflects verified objects, resumable sessions recover and expire safely when enabled, private access expires, and upload/delete/session events are audited.
