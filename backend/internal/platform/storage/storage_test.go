package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestFilePolicyAllowsArbitraryTypesButBoundsKeyAndSize(t *testing.T) {
	policy := DefaultFilePolicy()
	cases := []struct {
		request UploadRequest
		err     error
	}{
		{UploadRequest{Key: "../secret", ContentType: "image/png", Size: 1}, ErrInvalidKey},
		{UploadRequest{Key: "huge.png", ContentType: "image/png", Size: policy.MaxBytes + 1}, ErrTooLarge},
	}
	for _, item := range cases {
		if err := validateUpload(policy, item.request); !errors.Is(err, item.err) {
			t.Fatalf("request %#v: error = %v", item.request, err)
		}
	}
	if err := validateUpload(policy, UploadRequest{
		Key: "documents/example.svg", ContentType: "image/svg+xml", Size: 1,
	}); err != nil {
		t.Fatalf("arbitrary file type was rejected: %v", err)
	}
}

func TestLocalStorageContract(t *testing.T) {
	store, err := NewLocal(t.TempDir(), "/api/v1/files/upload", "/api/v1/files", DefaultImagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("\x89PNG\r\n\x1a\nexample")
	if _, err := store.Put(
		context.Background(), "users/avatar.png", bytes.NewReader(content), int64(len(content)),
	); err != nil {
		t.Fatal(err)
	}
	info, err := store.Stat(context.Background(), "users/avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(content)) || info.ContentType != StoredContentType {
		t.Fatalf("info = %#v", info)
	}
	reader, err := store.Open(context.Background(), "users/avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !bytes.Equal(opened, content) {
		t.Fatalf("opened content = %q", opened)
	}
	signed, err := store.SignRead(context.Background(), "users/avatar.png", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Method != "GET" || signed.URL == "" {
		t.Fatalf("signed = %#v", signed)
	}
	if err := store.Delete(context.Background(), "users/avatar.png"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "users/avatar.png"); err != nil {
		t.Fatalf("repeated delete must be idempotent: %v", err)
	}
	if _, err := store.Stat(context.Background(), "users/avatar.png"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat after delete = %v", err)
	}
}
