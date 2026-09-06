package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"gorm.io/gorm"
)

const sensitiveProviderError = "https://bucket.example/private.png?X-Amz-Credential=AKIA-SECRET&X-Amz-Signature=signed-url-secret"

func TestDatabaseErrorsDoNotLeakThroughHTTPOrDiagnosticLogs(
	t *testing.T,
) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	logs := captureUnredactedTestLogger(t)

	const callback = "test:inject-sensitive-product-error"
	if err := db.Callback().Query().Before("gorm:query").Register(
		callback,
		func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == "products" {
				tx.AddError(errors.New(sensitiveProviderError))
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callback)
	})

	response := serveRequest(
		server,
		cookie,
		http.MethodGet,
		"/api/v1/products",
		nil,
		"",
	)
	assertSensitiveFailureRedacted(
		t,
		response,
		logs.String(),
		"list_products_count",
	)
}

func TestStorageProviderErrorsDoNotLeakThroughHTTPOrDiagnosticLogs(
	t *testing.T,
) {
	cfg, _, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	confirmation := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirmation.Code != http.StatusOK {
		t.Fatalf(
			"confirm status = %d, body = %s",
			confirmation.Code,
			confirmation.Body.String(),
		)
	}

	server.store = signReadFailureStore{
		ObjectStore: server.store,
		err:         errors.New(sensitiveProviderError),
	}
	logs := captureUnredactedTestLogger(t)
	response := serveRequest(
		server,
		cookie,
		http.MethodGet,
		"/api/v1/files/"+prepared.File.ID+"/url",
		nil,
		"",
	)
	assertSensitiveFailureRedacted(
		t,
		response,
		logs.String(),
		"storage_sign_read",
	)
}

func TestBindingErrorsUseFixedPublicDetailAndRedactedDiagnostic(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test",
		nil,
	)
	context.Header("X-Request-ID", "request-redaction-test")
	logs := captureUnredactedTestLogger(t)

	writeBindingProblem(
		context,
		"Invalid test request",
		errors.New(sensitiveProviderError),
	)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"binding status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	assertSensitiveFailureRedacted(
		t,
		response,
		logs.String(),
		"decode_request_body",
	)
	if !strings.Contains(
		response.Body.String(),
		"The request body is invalid.",
	) {
		t.Fatalf("binding response detail = %s", response.Body.String())
	}
}

type signReadFailureStore struct {
	frameworkstorage.ObjectStore
	err error
}

func (store signReadFailureStore) SignRead(
	context.Context,
	string,
	time.Duration,
) (frameworkstorage.SignedRequest, error) {
	return frameworkstorage.SignedRequest{}, store.err
}

func captureUnredactedTestLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(
		slog.New(slog.NewTextHandler(&output, nil)),
	)
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	return &output
}

func assertSensitiveFailureRedacted(
	t *testing.T,
	response *httptest.ResponseRecorder,
	logs string,
	operation string,
) {
	t.Helper()
	if response.Code < http.StatusBadRequest {
		t.Fatalf(
			"failure status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if strings.Contains(response.Body.String(), sensitiveProviderError) ||
		strings.Contains(response.Body.String(), "signed-url-secret") {
		t.Fatalf("sensitive error leaked in response body: %s", response.Body.String())
	}
	for name, values := range response.Header() {
		for _, value := range values {
			if strings.Contains(value, sensitiveProviderError) ||
				strings.Contains(value, "signed-url-secret") {
				t.Fatalf(
					"sensitive error leaked in response header %s: %s",
					name,
					value,
				)
			}
		}
	}
	if strings.Contains(logs, sensitiveProviderError) ||
		strings.Contains(logs, "signed-url-secret") {
		t.Fatalf("sensitive error leaked in diagnostic log: %s", logs)
	}
	if !strings.Contains(logs, "operation="+operation) ||
		!strings.Contains(logs, "error="+httpx.RedactedValue) {
		t.Fatalf(
			"redacted diagnostic missing operation/error fields: %s",
			logs,
		)
	}
}
