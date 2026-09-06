package storage

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func TestOSSBrowserUploadReadinessUsesRealPreflightContract(t *testing.T) {
	t.Parallel()

	var requestedHeaders []string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodOptions {
			t.Errorf("method = %s, want OPTIONS", request.Method)
		}
		if got := request.Header.Get("Origin"); got != "https://admin.example.com" {
			t.Errorf("origin = %q", got)
		}
		if got := request.Header.Get("Access-Control-Request-Method"); got != http.MethodPut {
			t.Errorf("requested method = %q", got)
		}
		requestedHeaders = strings.Split(
			request.Header.Get("Access-Control-Request-Headers"),
			",",
		)
		writer.Header().Set("Access-Control-Allow-Origin", "https://admin.example.com")
		writer.Header().Set("Access-Control-Allow-Methods", "PUT")
		writer.Header().Set("Access-Control-Allow-Headers", strings.Join(requestedHeaders, ","))
		writer.Header().Set("Access-Control-Expose-Headers", "ETag, x-oss-request-id")
		writer.WriteHeader(http.StatusOK)
	})
	store := newBrowserReadinessTestOSS(t, handler)

	if err := store.CheckBrowserUploadReadiness(
		context.Background(),
		[]string{"https://admin.example.com"},
	); err != nil {
		t.Fatal(err)
	}
	want := []string{"content-disposition", "content-type", "x-oss-forbid-overwrite"}
	if strings.Join(requestedHeaders, ",") != strings.Join(want, ",") {
		t.Fatalf("preflight headers = %q, want %q", requestedHeaders, want)
	}
}

func TestOSSBrowserUploadReadinessRejectsMissingCORS(t *testing.T) {
	t.Parallel()

	store := newBrowserReadinessTestOSS(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`<Error><Code>AccessForbidden</Code><Message>CORSResponse: CORS is not enabled for this bucket.</Message></Error>`))
	}))

	err := store.CheckBrowserUploadReadiness(
		context.Background(),
		[]string{"http://localhost:3000"},
	)
	if !errors.Is(err, ErrBrowserUploadCORS) {
		t.Fatalf("error = %v, want ErrBrowserUploadCORS", err)
	}
}

func newBrowserReadinessTestOSS(t *testing.T, handler http.Handler) *OSS {
	t.Helper()
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(
			osscredentials.NewStaticCredentialsProvider("access-key", "secret-key"),
		).
		WithRegion("cn-hangzhou").
		WithEndpoint("https://objects.example.com").
		WithHttpClient(handlerHTTPClient(handler)).
		WithUsePathStyle(true)
	return NewOSS(cfg, "private-bucket", DefaultFilePolicy())
}
