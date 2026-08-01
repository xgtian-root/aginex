package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/framework/observability"
)

type storageObservationSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (sink *storageObservationSink) RecordSpan(
	record observability.SpanRecord,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, record)
}

func (sink *storageObservationSink) RecordMetric(
	metric observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.metrics = append(sink.metrics, metric)
}

func (sink *storageObservationSink) snapshot() (
	[]observability.SpanRecord,
	[]observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]observability.SpanRecord(nil), sink.spans...),
		append([]observability.Metric(nil), sink.metrics...)
}

func TestObserveStorageRecordsSafeOperationsAndUnwrapsLocal(t *testing.T) {
	local, err := NewLocal(
		t.TempDir(),
		"https://private.example/upload",
		"https://private.example/content",
		DefaultImagePolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	secretKey := "users/TOP-SECRET-filename.png"
	content := []byte("\x89PNG\r\n\x1a\nexample")
	if err := local.Put(secretKey, content, "image/png"); err != nil {
		t.Fatal(err)
	}
	sink := &storageObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, root := recorder.Start(
		context.Background(),
		observability.SpanStart{
			Name: "test request",
			Kind: observability.SpanKindServer,
		},
	)
	store := Observe(local, "local", recorder)
	if resolved, ok := AsLocal(store); !ok || resolved != local {
		t.Fatal("AsLocal did not unwrap the observed local store")
	}
	if repeated := Observe(store, "local", recorder); repeated != store {
		t.Fatal("repeated observation wrapped the store again")
	}
	checker, ok := store.(ReadinessChecker)
	if !ok {
		t.Fatal("observed local storage lost readiness support")
	}
	if err := checker.CheckReadiness(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUpload(ctx, UploadRequest{
		Key:         secretKey,
		ContentType: "image/png",
		Size:        int64(len(content)),
		Expires:     time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SignRead(ctx, secretKey, time.Minute); err != nil {
		t.Fatal(err)
	}
	info, err := store.Stat(ctx, secretKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("stat size = %d", info.Size)
	}
	reader, err := store.Open(ctx, secretKey)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, content) {
		t.Fatalf("opened content = %q", opened)
	}
	if err := store.Delete(ctx, secretKey); err != nil {
		t.Fatal(err)
	}

	spans, metrics := sink.snapshot()
	wantOperations := map[string]int{
		"readiness":     1,
		"create_upload": 1,
		"sign_read":     1,
		"stat":          1,
		"open":          1,
		"delete":        1,
	}
	gotOperations := make(map[string]int)
	for _, span := range spans {
		if span.Kind != observability.SpanKindClient {
			t.Fatalf("storage span kind = %q", span.Kind)
		}
		if !span.HasParent || span.Parent.SpanID() != root.Context().SpanID() {
			t.Fatal("storage span did not inherit the request span")
		}
		values := span.Attributes.Values()
		assertStorageAttributeKeys(t, values, "local")
		gotOperations[values["storage.operation"]]++
		if span.Error != "" {
			t.Fatalf("storage error text escaped into telemetry: %q", span.Error)
		}
	}
	if fmt.Sprint(gotOperations) != fmt.Sprint(wantOperations) {
		t.Fatalf("storage operation counts = %v, want %v", gotOperations, wantOperations)
	}
	bytesMetrics := 0
	for _, metric := range metrics {
		switch metric.Name {
		case "storage.client.operations", "storage.client.duration":
		case "storage.client.bytes":
			bytesMetrics++
		default:
			t.Fatalf("unexpected storage metric %q", metric.Name)
		}
		assertStorageAttributeKeys(t, metric.Attributes.Values(), "local")
	}
	if bytesMetrics != 3 {
		t.Fatalf("storage bytes metrics = %d, want 3", bytesMetrics)
	}
	assertStorageTelemetryExcludes(
		t,
		spans,
		metrics,
		secretKey,
		"private.example",
		"filename.png",
	)
	root.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
}

func TestObserveStoragePreservesErrorsWithoutExportingThem(t *testing.T) {
	sensitiveError := errors.New(
		"https://secret-bucket.example/private-key?token=TOP-SECRET-token",
	)
	inner := &failingStorage{err: sensitiveError}
	sink := &storageObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	store := Observe(inner, "secret-bucket-name", recorder)
	if _, ok := store.(ReadinessChecker); ok {
		t.Fatal("decorator added readiness to a store that does not support it")
	}
	if _, err := store.Stat(context.Background(), "private-key"); !errors.Is(
		err,
		sensitiveError,
	) {
		t.Fatalf("Stat error = %v", err)
	}
	spans, metrics := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("storage spans = %d, want 1", len(spans))
	}
	if spans[0].Outcome != observability.OutcomeError ||
		spans[0].Error != "" {
		t.Fatalf("error span = %+v", spans[0])
	}
	assertStorageAttributeKeys(
		t,
		spans[0].Attributes.Values(),
		"custom",
	)
	assertStorageTelemetryExcludes(
		t,
		spans,
		metrics,
		"secret-bucket",
		"private-key",
		"TOP-SECRET",
		"https://",
	)
}

type failingStorage struct {
	err error
}

func (storage *failingStorage) CreateUpload(
	context.Context,
	UploadRequest,
) (SignedRequest, error) {
	return SignedRequest{}, storage.err
}

func (storage *failingStorage) SignRead(
	context.Context,
	string,
	time.Duration,
) (SignedRequest, error) {
	return SignedRequest{}, storage.err
}

func (storage *failingStorage) Stat(
	context.Context,
	string,
) (ObjectInfo, error) {
	return ObjectInfo{}, storage.err
}

func (storage *failingStorage) Open(
	context.Context,
	string,
) (io.ReadCloser, error) {
	return nil, storage.err
}

func (storage *failingStorage) Delete(context.Context, string) error {
	return storage.err
}

func assertStorageAttributeKeys(
	t *testing.T,
	values map[string]string,
	provider string,
) {
	t.Helper()
	if len(values) != 3 ||
		values["storage.provider"] != provider ||
		values["storage.operation"] == "" ||
		values["outcome"] == "" {
		t.Fatalf("storage attributes = %v", values)
	}
	for key := range values {
		switch key {
		case "storage.provider", "storage.operation", "outcome":
		default:
			t.Fatalf("unexpected storage attribute %q", key)
		}
	}
}

func assertStorageTelemetryExcludes(
	t *testing.T,
	spans []observability.SpanRecord,
	metrics []observability.Metric,
	forbidden ...string,
) {
	t.Helper()
	var rendered strings.Builder
	for _, span := range spans {
		fmt.Fprintf(
			&rendered,
			"%s %s %s %v",
			span.Name,
			span.Error,
			span.Outcome,
			span.Attributes.Values(),
		)
	}
	for _, metric := range metrics {
		fmt.Fprintf(
			&rendered,
			"%s %v",
			metric.Name,
			metric.Attributes.Values(),
		)
	}
	output := rendered.String()
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("telemetry contains forbidden value %q: %s", value, output)
		}
	}
}
