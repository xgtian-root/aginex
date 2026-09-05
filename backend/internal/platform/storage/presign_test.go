package storage

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3CreateUploadBindsDeclaredSizeToSignature(t *testing.T) {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.com"),
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		),
		Region:       "us-east-1",
		UsePathStyle: true,
	})
	store := NewS3(client, "private-bucket", testMultipartPolicy(t))

	assertCreateUploadBindsDeclaredSize(t, store, "X-Amz-SignedHeaders")
	assertS3CreateUploadForbidsOverwrite(t, store)
	assertMultipartPartSignature(t, store, "X-Amz-SignedHeaders")
	assertControlledReadSignature(t, store)
}

func assertS3CreateUploadForbidsOverwrite(t *testing.T, store Storage) {
	t.Helper()
	signed, err := store.CreateUpload(context.Background(), UploadRequest{
		Key: "uploads/non-overwritable-object", Size: 4097, Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(signed.Headers, "If-None-Match"); got != "*" {
		t.Fatalf("signed If-None-Match = %q, want *", got)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToLower(parsed.Query().Get("X-Amz-SignedHeaders")); !strings.Contains(got, "if-none-match") {
		t.Fatalf("X-Amz-SignedHeaders = %q, want if-none-match bound to signature", got)
	}
}

func TestOSSCreateUploadBindsDeclaredSizeToSignature(t *testing.T) {
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(
			osscredentials.NewStaticCredentialsProvider("access-key", "secret-key"),
		).
		WithRegion("cn-hangzhou").
		WithEndpoint("https://objects.example.com")
	store := NewOSS(cfg, "private-bucket", testMultipartPolicy(t))

	assertCreateUploadBindsDeclaredSize(t, store, "x-oss-additional-headers")
	assertOSSCreateUploadForbidsOverwrite(t, store)
	assertMultipartPartSignature(t, store, "x-oss-additional-headers")
	assertControlledReadSignature(t, store)
}

func assertOSSCreateUploadForbidsOverwrite(t *testing.T, store Storage) {
	t.Helper()
	signed, err := store.CreateUpload(context.Background(), UploadRequest{
		Key: "uploads/non-overwritable-object", Size: 4097, Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(signed.Headers, "x-oss-forbid-overwrite"); got != "true" {
		t.Fatalf("signed x-oss-forbid-overwrite = %q, want true", got)
	}
}

func assertCreateUploadBindsDeclaredSize(
	t *testing.T,
	store Storage,
	signedHeadersParameter string,
) {
	t.Helper()
	const declaredSize int64 = 4097

	signed, err := store.CreateUpload(context.Background(), UploadRequest{
		Key:         "uploads/random-object.png",
		ContentType: "image/png",
		Size:        declaredSize,
		Expires:     time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerValue(signed.Headers, "Content-Length"); got != strconv.FormatInt(declaredSize, 10) {
		t.Fatalf("signed Content-Length = %q, want %d", got, declaredSize)
	}
	if got := headerValue(signed.Headers, "Content-Type"); got != StoredContentType {
		t.Fatalf("signed Content-Type = %q, want %q", got, StoredContentType)
	}
	if got := headerValue(signed.Headers, "Content-Disposition"); got != "attachment" {
		t.Fatalf("signed Content-Disposition = %q, want attachment", got)
	}
	if signedHeadersParameter != "" {
		parsed, err := url.Parse(signed.URL)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.ToLower(parsed.Query().Get(signedHeadersParameter)); !strings.Contains(got, "content-length") {
			t.Fatalf("%s = %q, want content-length bound to signature", signedHeadersParameter, got)
		}
	}
}

func assertMultipartPartSignature(
	t *testing.T,
	store Storage,
	signedHeadersParameter string,
) {
	t.Helper()
	multipart, ok := AsMultipart(store)
	if !ok {
		t.Fatal("store does not expose multipart capability")
	}
	const partSize int64 = 32 << 20
	signed, err := multipart.SignUploadPart(context.Background(), MultipartPartRequest{
		Upload:     MultipartUpload{Key: "uploads/arbitrary.bin", ProviderUploadID: "opaque-provider-upload-id"},
		PartNumber: 7, Size: partSize, Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("uploadId") != "opaque-provider-upload-id" || parsed.Query().Get("partNumber") != "7" {
		t.Fatalf("signed part URL = %q", signed.URL)
	}
	if got := headerValue(signed.Headers, "Content-Length"); got != strconv.FormatInt(partSize, 10) {
		t.Fatalf("part Content-Length = %q, want %d", got, partSize)
	}
	if signedHeadersParameter != "" {
		if got := strings.ToLower(parsed.Query().Get(signedHeadersParameter)); !strings.Contains(got, "content-length") {
			t.Fatalf("%s = %q, want content-length bound to part signature", signedHeadersParameter, got)
		}
	}
}

func assertControlledReadSignature(t *testing.T, store Storage) {
	t.Helper()
	controlled, ok := AsControlledRead(store)
	if !ok {
		t.Fatal("store does not expose controlled read capability")
	}
	inline, err := controlled.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "uploads/verified.pdf", ContentType: "application/pdf",
		Disposition: ReadDispositionInline, Filename: "季度报告.pdf", Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(inline.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("response-content-type"); got != "application/pdf" {
		t.Fatalf("response content type = %q", got)
	}
	if got := parsed.Query().Get("response-content-disposition"); !strings.HasPrefix(got, "inline") || !strings.Contains(strings.ToLower(got), "filename*=") {
		t.Fatalf("response content disposition = %q", got)
	}
	if _, err := controlled.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "uploads/unsafe.html", ContentType: "text/html",
		Disposition: ReadDispositionInline, Filename: "unsafe.html",
	}); err != ErrUnsafeInlineType {
		t.Fatalf("unsafe inline error = %v", err)
	}
	attachment, err := controlled.SignControlledRead(context.Background(), ControlledReadRequest{
		Key: "uploads/unsafe.html", ContentType: "text/html",
		Disposition: ReadDispositionAttachment, Filename: "download.html",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = url.Parse(attachment.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("response-content-type"); got != StoredContentType {
		t.Fatalf("attachment response content type = %q", got)
	}
	if got := parsed.Query().Get("response-content-disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("attachment response content disposition = %q", got)
	}
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func testMultipartPolicy(t *testing.T) FilePolicy {
	t.Helper()
	policy, err := NewFilePolicy(64 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
