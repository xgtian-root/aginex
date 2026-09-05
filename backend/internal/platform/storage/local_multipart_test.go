package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLocalMultipartStreamsListsCompletesAndAbortsIdempotently(t *testing.T) {
	t.Parallel()

	policy, err := NewFilePolicy(128)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocal(t.TempDir(), "/api/v1/files/local-upload", "/api/v1/files/local-content", policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := []byte("first part-")
	second := []byte("second part")
	upload, err := store.InitiateMultipart(ctx, UploadRequest{
		Key: "files/arbitrary.bin", Size: int64(len(first) + len(second)), ContentType: "text/html",
	})
	if err != nil {
		t.Fatal(err)
	}

	signed, err := store.SignUploadPart(ctx, MultipartPartRequest{
		Upload: upload, PartNumber: 2, Size: int64(len(second)), Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("uploadId") != upload.ProviderUploadID || parsed.Query().Get("partNumber") != "2" ||
		headerValue(signed.Headers, "Content-Type") != StoredContentType ||
		headerValue(signed.Headers, "Content-Length") != strconv.Itoa(len(second)) {
		t.Fatalf("signed part = %#v", signed)
	}

	secondPart, err := store.PutMultipartPart(ctx, MultipartPartRequest{
		Upload: upload, PartNumber: 2, Size: int64(len(second)),
	}, bytes.NewReader(second))
	if err != nil {
		t.Fatal(err)
	}
	firstPart, err := store.PutMultipartPart(ctx, MultipartPartRequest{
		Upload: upload, PartNumber: 1, Size: int64(len(first)),
	}, bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListUploadedParts(ctx, upload)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0] != firstPart || listed[1] != secondPart {
		t.Fatalf("listed parts = %#v", listed)
	}

	info, err := store.CompleteMultipart(ctx, MultipartCompleteRequest{
		Upload: upload, Parts: []UploadedPart{firstPart, secondPart},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(first)+len(second)) || info.ContentType != StoredContentType || info.ETag == "" {
		t.Fatalf("completed info = %#v", info)
	}
	reader, err := store.Open(ctx, upload.Key)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, append(append([]byte(nil), first...), second...)) {
		t.Fatalf("completed content = %q", content)
	}
	if _, err := store.CompleteMultipart(ctx, MultipartCompleteRequest{
		Upload: upload, Parts: []UploadedPart{firstPart, secondPart},
	}); err != nil {
		t.Fatalf("repeated complete failed: %v", err)
	}
	if err := store.AbortMultipart(ctx, upload); err != nil {
		t.Fatalf("abort after complete failed: %v", err)
	}

	cancelled, err := store.InitiateMultipart(ctx, UploadRequest{Key: "files/cancelled.bin", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AbortMultipart(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	if err := store.AbortMultipart(ctx, cancelled); err != nil {
		t.Fatalf("repeated abort failed: %v", err)
	}
}

func TestLocalPutAndPartRejectSizeMismatchWithoutPublishing(t *testing.T) {
	t.Parallel()

	store, err := NewLocal(t.TempDir(), "/upload", "/content", DefaultFilePolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.Put(ctx, "files/wrong.bin", bytes.NewReader([]byte("too long")), 3); !errors.Is(err, ErrFileSizeMismatch) {
		t.Fatalf("put error = %v", err)
	}
	if _, err := store.Stat(ctx, "files/wrong.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("mismatched object was published: %v", err)
	}

	upload, err := store.InitiateMultipart(ctx, UploadRequest{Key: "files/parts.bin", Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutMultipartPart(ctx, MultipartPartRequest{
		Upload: upload, PartNumber: 1, Size: 2,
	}, bytes.NewReader([]byte("abc"))); !errors.Is(err, ErrFileSizeMismatch) {
		t.Fatalf("part error = %v", err)
	}
	parts, err := store.ListUploadedParts(ctx, upload)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 0 {
		t.Fatalf("mismatched part was published: %#v", parts)
	}
}

func TestLocalSinglePutNeverOverwritesPublishedBytes(t *testing.T) {
	t.Parallel()

	store, err := NewLocal(t.TempDir(), "/upload", "/content", DefaultFilePolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := []byte("first")
	second := []byte("other")
	if _, err := store.Put(ctx, "files/no-replace", bytes.NewReader(first), int64(len(first))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, "files/no-replace", bytes.NewReader(second), int64(len(second))); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second put error = %v, want already exists", err)
	}
	reader, err := store.Open(ctx, "files/no-replace")
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, first) {
		t.Fatalf("published content = %q, want %q", content, first)
	}
}

func TestLocalControlledReadCarriesOnlyValidatedPurpose(t *testing.T) {
	t.Parallel()

	store, err := NewLocal(t.TempDir(), "/upload", "/content", DefaultFilePolicy())
	if err != nil {
		t.Fatal(err)
	}
	inline, err := store.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "files/safe.pdf", ContentType: "application/pdf", Disposition: ReadDispositionInline,
		Filename: "report.pdf", Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(inline.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("purpose") != "preview" || parsed.Query().Has("contentType") || parsed.Query().Has("filename") {
		t.Fatalf("inline URL = %q", inline.URL)
	}
	if _, err := store.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "files/unsafe.html", ContentType: "text/html", Disposition: ReadDispositionInline,
		Filename: "unsafe.html",
	}); !errors.Is(err, ErrUnsafeInlineType) {
		t.Fatalf("unsafe inline error = %v", err)
	}
	if _, err := store.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "files/safe.pdf", ContentType: "application/pdf", Disposition: ReadDispositionAttachment,
		Filename: "bad\r\nname.pdf",
	}); !errors.Is(err, ErrInvalidFilename) {
		t.Fatalf("filename error = %v", err)
	}
}

func TestLocalMultipartStagingMaintenanceIsBoundedAndCanonical(t *testing.T) {
	t.Parallel()

	store, err := NewLocal(t.TempDir(), "/upload", "/content", DefaultFilePolicy())
	if err != nil {
		t.Fatal(err)
	}
	upload, err := store.InitiateMultipart(context.Background(), UploadRequest{
		Key: "files/orphan", Size: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-49 * time.Hour)
	directory := filepath.Join(store.root, localMultipartDirectory, upload.ProviderUploadID)
	if err := os.Chtimes(directory, old, old); err != nil {
		t.Fatal(err)
	}
	staging, err := store.ListMultipartStaging(context.Background(), time.Now().UTC().Add(-48*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(staging) != 1 || staging[0].ProviderUploadID != upload.ProviderUploadID {
		t.Fatalf("staging = %#v", staging)
	}
	if _, err := store.ListMultipartStaging(context.Background(), time.Now(), 101); !errors.Is(err, ErrInvalidMultipart) {
		t.Fatalf("unbounded list error = %v", err)
	}
	if err := store.RemoveMultipartStaging(context.Background(), upload.ProviderUploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("staging still exists: %v", err)
	}
	if err := store.RemoveMultipartStaging(context.Background(), "../escape"); !errors.Is(err, ErrInvalidMultipart) {
		t.Fatalf("invalid staging removal error = %v", err)
	}
}
