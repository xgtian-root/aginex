package storage

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/internal/config"
)

func TestS3CompatibleStorageContract(t *testing.T) {
	cfg, ok := cloudTestConfig(t, "S3")
	if !ok {
		return
	}
	runCloudStorageContract(t, cfg)
}

func TestAlibabaOSSStorageContract(t *testing.T) {
	cfg, ok := cloudTestConfig(t, "OSS")
	if !ok {
		return
	}
	runCloudStorageContract(t, cfg)
}

func cloudTestConfig(t *testing.T, provider string) (config.Storage, bool) {
	t.Helper()
	prefix := "AGINEX_TEST_" + provider + "_"
	required := strings.EqualFold(
		strings.TrimSpace(os.Getenv("AGINEX_REQUIRE_"+provider+"_CONTRACT")),
		"true",
	)
	values := map[string]string{
		"endpoint":   strings.TrimSpace(os.Getenv(prefix + "ENDPOINT")),
		"bucket":     strings.TrimSpace(os.Getenv(prefix + "BUCKET")),
		"region":     strings.TrimSpace(os.Getenv(prefix + "REGION")),
		"access_key": strings.TrimSpace(os.Getenv(prefix + "ACCESS_KEY_ID")),
		"secret_key": strings.TrimSpace(os.Getenv(prefix + "ACCESS_KEY_SECRET")),
	}
	var missing []string
	for name, value := range values {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		message := fmt.Sprintf(
			"%s storage contract unavailable; missing %s",
			provider,
			strings.Join(missing, ", "),
		)
		if required {
			t.Fatal(message)
		}
		t.Skip(message)
		return config.Storage{}, false
	}
	return config.Storage{
		Driver:          strings.ToLower(provider),
		Endpoint:        values["endpoint"],
		Bucket:          values["bucket"],
		Region:          values["region"],
		AccessKeyID:     values["access_key"],
		AccessKeySecret: values["secret_key"],
	}, true
}

func runCloudStorageContract(t *testing.T, cfg config.Storage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	store, err := FromConfig(ctx, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	key := "aginex-contract/" + uuid.NewString() + ".png"
	content := contractPNG(t)
	defer func() {
		_ = store.Delete(context.Background(), key)
	}()

	upload, err := store.CreateUpload(ctx, UploadRequest{
		Key:         key,
		ContentType: "image/png",
		Size:        int64(len(content)),
		Expires:     5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(upload.Headers, "Content-Length"); got != strconv.FormatInt(int64(len(content)), 10) {
		t.Fatalf(
			"signed upload Content-Length = %q, want %d",
			got,
			len(content),
		)
	}
	putRequest, err := http.NewRequestWithContext(
		ctx,
		upload.Method,
		upload.URL,
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatal(err)
	}
	putRequest.ContentLength = int64(len(content))
	for name, value := range upload.Headers {
		putRequest.Header.Set(name, value)
	}
	putResponse, err := cloudContractHTTPClient().Do(putRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(putResponse.Body, 64<<10))
	if closeErr := putResponse.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if putResponse.StatusCode < 200 || putResponse.StatusCode >= 300 {
		t.Fatalf("signed upload status = %d", putResponse.StatusCode)
	}

	info, err := store.Stat(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != key ||
		info.Size != int64(len(content)) ||
		!strings.HasPrefix(strings.ToLower(info.ContentType), "image/png") {
		t.Fatalf("object info = %#v", info)
	}

	reader, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := io.ReadAll(io.LimitReader(reader, int64(len(content))+1))
	closeErr := reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !bytes.Equal(opened, content) {
		t.Fatal("Open returned different object content")
	}

	download, err := store.SignRead(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	getRequest, err := http.NewRequestWithContext(ctx, download.Method, download.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range download.Headers {
		getRequest.Header.Set(name, value)
	}
	getResponse, err := cloudContractHTTPClient().Do(getRequest)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, readErr := io.ReadAll(io.LimitReader(getResponse.Body, int64(len(content))+1))
	closeErr = getResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("signed download status = %d", getResponse.StatusCode)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatal("signed download returned different object content")
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("repeated delete must be idempotent: %v", err)
	}
	if _, err := store.Stat(ctx, key); err == nil {
		t.Fatal("deleted object is still visible")
	}
}

func cloudContractHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

func contractPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.NRGBA{R: 215, G: 45, B: 75, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, picture); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
