package ratelimit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/xgtian-root/aginex/server/framework/observability"
)

type observationSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (sink *observationSink) RecordSpan(record observability.SpanRecord) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, record)
}

func (sink *observationSink) RecordMetric(metric observability.Metric) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.metrics = append(sink.metrics, metric)
}

func (sink *observationSink) snapshot() (
	[]observability.SpanRecord,
	[]observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]observability.SpanRecord(nil), sink.spans...),
		append([]observability.Metric(nil), sink.metrics...)
}

type observationLimiter struct{}

func (observationLimiter) Consume(
	ctx context.Context,
	request Request,
) (Decision, error) {
	if _, ok := observability.SpanContextFromContext(ctx); !ok {
		return Decision{}, errors.New("span context is missing")
	}
	switch request.Key {
	case "deny-secret-user":
		return Decision{Allowed: false}, nil
	case "error-secret-user":
		return Decision{}, errors.New("database exposed error-secret-user")
	default:
		return Decision{Allowed: true}, nil
	}
}

func (observationLimiter) CleanupExpired(
	context.Context,
	int,
) (int64, error) {
	return 3, nil
}

func TestObserveRecordsOutcomesWithoutSensitiveRequestDimensions(t *testing.T) {
	sink := &observationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := Observe(observationLimiter{}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Namespace: "login.ip", Key: "allow-secret-user"},
		{Namespace: "login.ip", Key: "deny-secret-user"},
		{Namespace: "login.ip", Key: "error-secret-user"},
	} {
		_, _ = limiter.Consume(context.Background(), request)
	}

	spans, metrics := sink.snapshot()
	if len(spans) != 3 {
		t.Fatalf("span count = %d, want 3", len(spans))
	}
	metricNames := make(map[string]int)
	for _, metric := range metrics {
		metricNames[metric.Name]++
		values := metric.Attributes.Values()
		if len(values) != 1 || values["namespace"] != "login.ip" {
			t.Fatalf("metric attributes = %#v, want namespace only", values)
		}
		assertNoRateLimitSecret(t, metric.Name, values, metric.Name)
	}
	for _, name := range []string{
		"ratelimit.consume.allowed",
		"ratelimit.consume.denied",
		"ratelimit.consume.error",
	} {
		if metricNames[name] != 1 {
			t.Fatalf("metric %q count = %d, want 1", name, metricNames[name])
		}
	}
	if metricNames["ratelimit.consume.duration"] != 3 {
		t.Fatalf(
			"duration count = %d, want 3",
			metricNames["ratelimit.consume.duration"],
		)
	}
	for _, span := range spans {
		values := span.Attributes.Values()
		if len(values) != 1 || values["namespace"] != "login.ip" {
			t.Fatalf("span attributes = %#v, want namespace only", values)
		}
		assertNoRateLimitSecret(t, span.Error, values, span.Name)
	}
	if spans[2].Outcome != observability.OutcomeError ||
		spans[2].Error != errObservedRateLimitOperation.Error() {
		t.Fatalf("error span = %#v", spans[2])
	}
}

func TestObserveCleanupAndInvalidNamespaceStayBounded(t *testing.T) {
	sink := &observationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := Observe(observationLimiter{}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = limiter.Consume(context.Background(), Request{
		Namespace: "SECRET\nnamespace",
		Key:       "error-secret-user",
	})
	removed, err := limiter.CleanupExpired(context.Background(), 10)
	if err != nil || removed != 3 {
		t.Fatalf("cleanup = %d, %v", removed, err)
	}

	spans, metrics := sink.snapshot()
	if got := spans[0].Attributes.Values()["namespace"]; got != "invalid" {
		t.Fatalf("invalid namespace observation = %q", got)
	}
	for _, metric := range metrics {
		if strings.HasPrefix(metric.Name, "ratelimit.cleanup.") &&
			metric.Attributes.Len() != 0 {
			t.Fatalf("cleanup metric attributes = %#v", metric.Attributes.Values())
		}
	}
}

func TestObserveRejectsNilLimiter(t *testing.T) {
	if _, err := Observe(nil, nil); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("Observe(nil) error = %v, want ErrInvalidOption", err)
	}
}

func TestObservedLimiterPreservesNilContextValidation(t *testing.T) {
	limiter, err := Observe(observationLimiter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Consume(nil, Request{}); !errors.Is(
		err,
		ErrInvalidRequest,
	) {
		t.Fatalf("Consume(nil) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := limiter.CleanupExpired(nil, 1); !errors.Is(
		err,
		ErrInvalidRequest,
	) {
		t.Fatalf(
			"CleanupExpired(nil) error = %v, want ErrInvalidRequest",
			err,
		)
	}
}

func assertNoRateLimitSecret(
	t *testing.T,
	errorText string,
	attributes map[string]string,
	name string,
) {
	t.Helper()
	all := errorText + name
	for key, value := range attributes {
		all += key + value
	}
	for _, secret := range []string{
		"allow-secret-user",
		"deny-secret-user",
		"error-secret-user",
		"database exposed",
	} {
		if strings.Contains(all, secret) {
			t.Fatalf("observation leaked %q: %q", secret, all)
		}
	}
}
