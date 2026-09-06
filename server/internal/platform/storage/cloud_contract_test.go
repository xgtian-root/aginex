package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/server/internal/config"
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

func TestConfiguredAlibabaOSSProviderRead(t *testing.T) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("AGINEX_REQUIRE_CONFIGURED_OSS_READ")), "true") {
		t.Skip("configured OSS read verification is opt-in")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Driver != "oss" {
		t.Fatalf("configured storage driver = %q, want oss", cfg.Storage.Driver)
	}
	key := strings.TrimSpace(os.Getenv("AGINEX_TEST_OSS_OBJECT_KEY"))
	if key == "" {
		t.Fatal("AGINEX_TEST_OSS_OBJECT_KEY is required")
	}
	store, err := FromConfig(t.Context(), cfg.Storage, cfg.HTTP.PublicURL)
	if err != nil {
		t.Fatal(err)
	}
	ossStore, ok := store.(*OSS)
	if !ok {
		t.Fatal("configured OSS store has an unexpected implementation")
	}
	cnameResult, err := ossStore.client.ListCname(t.Context(), &aliyunoss.ListCnameRequest{
		Bucket: aliyunoss.Ptr(cfg.Storage.Bucket),
	})
	if err != nil {
		t.Fatalf("list configured OSS CNAMEs: %v", err)
	}
	for _, cname := range cnameResult.Cnames {
		t.Logf("configured OSS CNAME domain=%q status=%q", aliyunoss.ToString(cname.Domain), aliyunoss.ToString(cname.Status))
	}
	controlled, ok := AsControlledRead(store)
	if !ok {
		t.Fatal("configured OSS store does not support controlled reads")
	}
	signed, err := controlled.SignControlledRead(t.Context(), ControlledReadRequest{
		Key: key, ContentType: "image/png", Disposition: ReadDispositionInline,
		Filename: "verified.png", Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	signedURL, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	accessURL, err := url.Parse(cfg.Storage.AccessBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if signedURL.Host != accessURL.Host || !strings.Contains(signedURL.Path, key) {
		t.Fatal("signed OSS read did not use the configured access origin and object key")
	}
	if !strings.HasPrefix(strings.ToLower(signedURL.Query().Get("response-content-disposition")), "inline") {
		t.Fatal("signed OSS read omitted the inline response disposition")
	}
	request, err := http.NewRequestWithContext(t.Context(), signed.Method, signed.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range signed.Headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close configured OSS response = %v/%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Disposition")), "inline") {
		t.Fatalf("configured OSS response status/type/disposition/force-download = %d/%q/%q/%q",
			response.StatusCode, response.Header.Get("Content-Type"), response.Header.Values("Content-Disposition"),
			response.Header.Get("x-oss-force-download"))
	}
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
	if provider == "OSS" {
		values["access_base_url"] = strings.TrimSpace(os.Getenv(prefix + "ACCESS_BASE_URL"))
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
		AccessBaseURL:   values["access_base_url"],
		ForcePathStyle:  values["endpoint"] != "" && provider == "S3",
	}, true
}

func runCloudStorageContract(t *testing.T, cfg config.Storage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	policy, err := NewFilePolicy(65 << 20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := FromConfig(ctx, cfg, "", policy)
	if err != nil {
		t.Fatal(err)
	}
	key := "aginex-contract/" + uuid.NewString()
	content := contractPNG(t)
	defer func() {
		_ = store.Delete(context.Background(), key)
	}()

	upload, err := store.CreateUpload(ctx, UploadRequest{
		Key:         key,
		ContentType: StoredContentType,
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
		!strings.HasPrefix(strings.ToLower(info.ContentType), StoredContentType) {
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

	runCloudMultipartContract(t, ctx, store)
}

func runCloudMultipartContract(
	t *testing.T,
	ctx context.Context,
	store Storage,
) {
	t.Helper()
	multipart, ok := AsMultipart(store)
	if !ok {
		t.Fatal("configured cloud store does not expose multipart capability")
	}
	key := "aginex-contract/multipart-" + uuid.NewString()
	first := bytes.Repeat([]byte{0x5a}, 32<<20)
	last := bytes.Repeat([]byte{0xa5}, 1<<20)
	total := int64(len(first) + len(last))
	defer func() { _ = store.Delete(context.Background(), key) }()

	upload, err := multipart.InitiateMultipart(ctx, UploadRequest{
		Key: key, ContentType: StoredContentType, Size: total, Expires: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = multipart.AbortMultipart(context.Background(), upload)
		}
	}()

	parts := []UploadedPart{
		uploadCloudContractPart(t, ctx, multipart, upload, 1, first),
		uploadCloudContractPart(t, ctx, multipart, upload, 2, last),
	}
	inventory, err := multipart.ListUploadedParts(ctx, upload)
	if err != nil {
		t.Fatal(err)
	}
	if !uploadedPartsMatch(parts, inventory) {
		t.Fatalf("provider multipart inventory = %#v, want %#v", inventory, parts)
	}

	info, err := multipart.CompleteMultipart(ctx, MultipartCompleteRequest{
		Upload: upload,
		Parts:  parts,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed = true
	if info.Key != key || info.Size != total {
		t.Fatalf("completed multipart object = %#v", info)
	}
	// A retry after a lost completion response must recover through final-object
	// Stat instead of treating the provider's missing upload session as failure.
	replayed, err := multipart.CompleteMultipart(ctx, MultipartCompleteRequest{
		Upload: upload,
		Parts:  parts,
	})
	if err != nil || replayed.Size != total {
		t.Fatalf("replayed multipart completion = %#v, %v", replayed, err)
	}

	reader, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	actualDigest := sha256.New()
	read, readErr := io.Copy(actualDigest, io.LimitReader(reader, total+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || read != total {
		t.Fatalf("read completed multipart bytes = %d, %v, %v", read, readErr, closeErr)
	}
	expectedDigest := sha256.New()
	_, _ = expectedDigest.Write(first)
	_, _ = expectedDigest.Write(last)
	if !bytes.Equal(actualDigest.Sum(nil), expectedDigest.Sum(nil)) {
		t.Fatal("completed multipart content digest differs")
	}

	abortUpload, err := multipart.InitiateMultipart(ctx, UploadRequest{
		Key: key + "-abort", ContentType: StoredContentType, Size: total, Expires: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := multipart.AbortMultipart(ctx, abortUpload); err != nil {
		t.Fatal(err)
	}
	if err := multipart.AbortMultipart(ctx, abortUpload); err != nil {
		t.Fatalf("repeated multipart abort must be idempotent: %v", err)
	}
}

func uploadCloudContractPart(
	t *testing.T,
	ctx context.Context,
	store MultipartObjectStore,
	upload MultipartUpload,
	partNumber int32,
	content []byte,
) UploadedPart {
	t.Helper()
	signed, err := store.SignUploadPart(ctx, MultipartPartRequest{
		Upload: upload, PartNumber: partNumber, Size: int64(len(content)), Expires: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(signed.Headers, "Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("part %d signed Content-Length = %q", partNumber, got)
	}
	request, err := http.NewRequestWithContext(ctx, signed.Method, signed.URL, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = int64(len(content))
	for name, value := range signed.Headers {
		request.Header.Set(name, value)
	}
	response, err := cloudContractHTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	closeErr := response.Body.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("part %d upload status = %d", partNumber, response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	if strings.TrimSpace(etag) == "" {
		t.Fatalf("part %d upload response omitted ETag", partNumber)
	}
	return UploadedPart{PartNumber: partNumber, Size: int64(len(content)), ETag: etag}
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
