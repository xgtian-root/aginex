package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/framework/observability"
	"github.com/xgtian-root/aginex/backend/internal/config"
)

type requestTraceSink struct {
	mu    sync.Mutex
	spans []observability.SpanRecord
}

func (sink *requestTraceSink) RecordSpan(span observability.SpanRecord) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, span)
}

func (*requestTraceSink) RecordMetric(observability.Metric) {}

func (sink *requestTraceSink) reset() {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = nil
}

func (sink *requestTraceSink) snapshot() []observability.SpanRecord {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]observability.SpanRecord(nil), sink.spans...)
}

func TestAuthenticatedRequestDatabaseSpansRemainInHTTPTrace(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "request-trace.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	sink := &requestTraceSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewWithModulesAndObservability(cfg, db, recorder)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, server)
	sink.reset()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"list status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	traceParent := response.Header().Get(observability.TraceParentHeader)
	parts := strings.Split(traceParent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 {
		t.Fatalf("response traceparent = %q", traceParent)
	}
	traceID := parts[1]

	var (
		foundServer bool
		foundDB     bool
	)
	for _, span := range sink.snapshot() {
		if span.Context.TraceID() != traceID {
			continue
		}
		if span.Kind == observability.SpanKindServer {
			foundServer = true
		}
		if span.Kind == observability.SpanKindClient &&
			strings.HasPrefix(span.Name, "db ") {
			foundDB = true
			if !span.HasParent ||
				span.Parent.TraceID() != traceID {
				t.Fatalf(
					"database span parent = %#v, want HTTP trace %s",
					span.Parent,
					traceID,
				)
			}
		}
	}
	if !foundServer || !foundDB {
		t.Fatalf(
			"trace %s server/database spans = %v/%v; spans = %#v",
			traceID,
			foundServer,
			foundDB,
			sink.snapshot(),
		)
	}
}
