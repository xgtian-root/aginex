---
name: add-image-upload
description: Add a safe Aginex image upload flow through the Storage abstraction with Local, S3-compatible, and Alibaba Cloud OSS providers, upload intent and confirmation, object verification, metadata persistence, signed access, permissions, and tests. Use for images, attachments, object storage, presigned uploads, S3, MinIO, R2, or OSS.
---

# Add Image Upload

## Workflow

1. Use the shared `Storage` interface; business modules must not import cloud SDKs.
2. Create an upload intent with an opaque object key and short expiration.
3. Enforce configured JPEG, PNG, and WebP MIME/size limits; reject SVG by default.
4. Let the browser upload directly for cloud providers, then confirm through the API.
5. On confirmation, Stat/Head the object and compare size, MIME, bucket/key, and checksum when available before marking metadata ready.
6. Use short-lived signed URLs for private reads. Represent delete failures as retryable state.
7. Protect intent, confirm, read, and delete operations with explicit permissions and audit writes.
8. Run Local and provider contract tests; use MinIO for S3-compatible CI.

## Constraints

- Never trust filename, browser MIME, bucket, key, or claimed size without verification.
- Never make credentials or private bucket URLs visible to the browser.
- Do not add transformation, multipart resume, or versioning to v1.

## Completion Gate

Local, S3-compatible, and OSS adapters satisfy the same contract, unsafe types fail, metadata reflects verified objects, private access expires, and upload/delete events are audited.
