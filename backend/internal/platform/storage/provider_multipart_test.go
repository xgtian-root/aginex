package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	providerTestKey      = "uploads/arbitrary.bin"
	providerTestUploadID = "opaque-upload-id"
	providerTestETag     = `"opaque-part-etag"`
	providerTestPartSize = int64(32 << 20)
)

func TestS3MultipartUsesOpaqueListedETagBeforeComplete(t *testing.T) {
	t.Parallel()

	httpClient := handlerHTTPClient(providerMultipartHandler(t, "s3"))
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.com"),
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		),
		Region: "us-east-1", UsePathStyle: true, HTTPClient: httpClient,
	})
	store := NewS3(client, "private-bucket", testMultipartPolicy(t))
	runProviderMultipartLifecycle(t, store)
}

func TestOSSMultipartUsesOpaqueListedETagBeforeComplete(t *testing.T) {
	t.Parallel()

	httpClient := handlerHTTPClient(providerMultipartHandler(t, "oss"))
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider("access-key", "secret-key")).
		WithRegion("cn-hangzhou").
		WithEndpoint("https://objects.example.com").
		WithHttpClient(httpClient).
		WithUsePathStyle(true)
	store := NewOSS(cfg, "private-bucket", testMultipartPolicy(t))
	runProviderMultipartLifecycle(t, store)
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (transport handlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func handlerHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: handlerRoundTripper{handler: handler}}
}

func runProviderMultipartLifecycle(t *testing.T, store MultipartObjectStore) {
	t.Helper()
	ctx := context.Background()
	upload, err := store.InitiateMultipart(ctx, UploadRequest{
		Key: providerTestKey, Size: providerTestPartSize, ContentType: "text/html",
	})
	if err != nil {
		t.Fatal(err)
	}
	if upload.ProviderUploadID != providerTestUploadID || upload.Key != providerTestKey {
		t.Fatalf("upload = %#v", upload)
	}
	parts, err := store.ListUploadedParts(ctx, upload)
	if err != nil {
		t.Fatal(err)
	}
	wantPart := UploadedPart{PartNumber: 1, Size: providerTestPartSize, ETag: providerTestETag}
	if len(parts) != 1 || parts[0] != wantPart {
		t.Fatalf("parts = %#v", parts)
	}
	info, err := store.CompleteMultipart(ctx, MultipartCompleteRequest{
		Upload: upload, Parts: []UploadedPart{wantPart},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != providerTestKey || info.Size != providerTestPartSize || info.ContentType != StoredContentType {
		t.Fatalf("completed info = %#v", info)
	}
	if err := store.AbortMultipart(ctx, upload); err != nil {
		t.Fatal(err)
	}
}

func providerMultipartHandler(t *testing.T, provider string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		switch {
		case request.Method == http.MethodPost && query.Has("uploads"):
			if request.Header.Get("Content-Type") != StoredContentType || request.Header.Get("Content-Disposition") != "attachment" {
				t.Errorf("%s initiate headers = %v", provider, request.Header)
			}
			writer.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(writer,
				"<InitiateMultipartUploadResult><Bucket>private-bucket</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>",
				providerTestKey, providerTestUploadID,
			)
		case request.Method == http.MethodGet && query.Get("uploadId") == providerTestUploadID:
			writer.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(writer,
				"<ListPartsResult><Bucket>private-bucket</Bucket><Key>%s</Key><UploadId>%s</UploadId><PartNumberMarker>0</PartNumberMarker><NextPartNumberMarker>0</NextPartNumberMarker><MaxParts>1000</MaxParts><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><LastModified>2026-08-11T00:00:00.000Z</LastModified><ETag>&quot;opaque-part-etag&quot;</ETag><Size>%d</Size></Part></ListPartsResult>",
				providerTestKey, providerTestUploadID, providerTestPartSize,
			)
		case request.Method == http.MethodPost && query.Get("uploadId") == providerTestUploadID:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("%s complete body: %v", provider, err)
			}
			if !strings.Contains(string(body), "opaque-part-etag") {
				t.Errorf("%s complete body did not preserve ETag: %s", provider, body)
			}
			writer.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(writer,
				"<CompleteMultipartUploadResult><Location>private</Location><Bucket>private-bucket</Bucket><Key>%s</Key><ETag>&quot;whole-etag&quot;</ETag></CompleteMultipartUploadResult>",
				providerTestKey,
			)
		case request.Method == http.MethodHead:
			writer.Header().Set("Content-Length", fmt.Sprint(providerTestPartSize))
			writer.Header().Set("Content-Type", StoredContentType)
			writer.Header().Set("ETag", `"whole-etag"`)
			writer.Header().Set("Last-Modified", time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
			writer.WriteHeader(http.StatusOK)
		case request.Method == http.MethodDelete && query.Get("uploadId") == providerTestUploadID:
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s request: %s %s", provider, request.Method, request.URL.String())
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	})
}
