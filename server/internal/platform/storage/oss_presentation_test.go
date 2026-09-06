package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func TestOSSFinalizesOnlyVerifiedSafePresentationMetadata(t *testing.T) {
	t.Parallel()

	var copied bool
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			copied = true
			if request.Header.Get("x-oss-metadata-directive") != "REPLACE" ||
				request.Header.Get("Content-Type") != "image/png" ||
				request.Header.Get("Content-Disposition") != "" ||
				request.Header.Get("x-oss-copy-source-if-match") != `"verified-etag"` ||
				!strings.Contains(request.Header.Get("x-oss-copy-source"), "uploads/verified.png") {
				t.Errorf("copy headers = %#v", request.Header)
			}
			writer.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(writer, `<CopyObjectResult><LastModified>2026-09-06T00:00:00.000Z</LastModified><ETag>&quot;final-etag&quot;</ETag></CopyObjectResult>`)
		case http.MethodHead:
			writer.Header().Set("Content-Length", "128")
			writer.Header().Set("Content-Type", "image/png")
			writer.Header().Set("ETag", `"final-etag"`)
			writer.Header().Set("Last-Modified", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
			http.Error(writer, "unexpected", http.StatusBadRequest)
		}
	})
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider("access-key", "secret-key")).
		WithRegion("cn-hangzhou").
		WithEndpoint("https://objects.example.com").
		WithHttpClient(handlerHTTPClient(handler)).
		WithUsePathStyle(true)
	store := NewOSS(cfg, "private-bucket", DefaultFilePolicy())

	info, err := store.FinalizeVerifiedPresentation(context.Background(), VerifiedPresentationRequest{
		Key: "uploads/verified.png", ContentType: "image/png", ExpectedETag: "verified-etag",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !copied || info.Key != "uploads/verified.png" || info.Size != 128 ||
		info.ContentType != "image/png" || info.ContentDisposition != "" || info.ETag != "final-etag" {
		t.Fatalf("finalized info = %#v, copied = %v", info, copied)
	}

	if _, err := store.FinalizeVerifiedPresentation(context.Background(), VerifiedPresentationRequest{
		Key: "uploads/unsafe.html", ContentType: "text/html",
	}); err != ErrUnsafeInlineType {
		t.Fatalf("unsafe presentation error = %v", err)
	}
}
